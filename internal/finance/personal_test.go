package finance

import (
	"context"
	"errors"
	"testing"
	"time"

	"openchris/conselho/internal/memory"
)

func TestBankClassificationSeparatesConsumptionAndMovement(t *testing.T) {
	for _, tt := range []struct{ category, direction, operation, want string }{
		{"Groceries", "DEBIT", "", "expense"},
		{"Same person transfer - PIX", "DEBIT", "", "transfer_internal"},
		{"Credit card payment", "DEBIT", "", "card_bill_payment"},
		{"", "CREDIT", "PAGAMENTO_FATURA", "card_bill_payment"},
		{"Fixed income", "DEBIT", "", "investment"},
		{"Margin", "DEBIT", "", "investment"},
		{"Pension", "DEBIT", "", "investment"},
		{"Pension", "CREDIT", "", "investment"},
		{"Salary", "CREDIT", "", "income"},
		{"Refund", "CREDIT", "", "refund"},
		{"Transfer - PIX", "DEBIT", "", "unknown"},
		{"", "", "", "unknown"},
	} {
		if got := bankKind(tt.category, tt.direction, tt.operation); got != tt.want {
			t.Errorf("%q/%q: got %s, want %s", tt.category, tt.direction, got, tt.want)
		}
	}
}

func TestExactMoneyDoesNotRoundOrInventUnknown(t *testing.T) {
	for _, tt := range []struct {
		raw   string
		cents int64
		valid bool
	}{{"0.10", 10, true}, {"0.20", 20, true}, {"-12.34", -1234, true}, {"1.001", 0, false}, {"NaN", 0, false}, {"", 0, false}, {"10000000001", 0, false}} {
		value, ok := exactCents(tt.raw)
		if ok != tt.valid || ok && value != tt.cents {
			t.Errorf("%s: %d %v", tt.raw, value, ok)
		}
	}
}

func completeProfile() Profile {
	zero, reserve, target := int64(0), int64(100000), int64(600000)
	days := 1
	return Profile{Confirmed: true, Goals: []Goal{{Name: "Reserva", TargetCents: 600000, TargetDate: "2027-12-31"}}, ReserveCurrentCents: &reserve, ReserveTargetCents: &target, Commitments30DaysCents: &zero, MinimumCheckingCents: &zero, ProtectedCheckingCents: &zero, RiskProfile: "conservative", Experience: "beginner", LiquidityDays: &days}
}

func TestProfileMissingIsNotZeroAndCashDoesNotSubtractInvestedReserve(t *testing.T) {
	p := completeProfile()
	if issues := profileIssues(p); len(issues) != 0 {
		t.Fatal(issues)
	}
	missing := p
	missing.Commitments30DaysCents = nil
	if len(profileIssues(missing)) == 0 {
		t.Fatal("unknown commitments treated as zero")
	}
	cash := int64(500000)
	available := availableCash(p, cash)
	if available != cash {
		t.Fatal("invested reserve was subtracted from checking", available)
	}
	obligations := int64(600000)
	p.Commitments30DaysCents = &obligations
	if availableCash(p, cash) != 0 {
		t.Fatal("committed cash became investible")
	}
	p.RiskProfile = "invented"
	if _, err := New(nil).SaveProfile(context.Background(), memory.UserID, p); !errors.Is(err, ErrValidation) {
		t.Fatal(err)
	}
}

func TestPersonalProjectionPreservesCorrectionsAndCompleteTotals(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	_, err := s.DB.Exec(ctx, `TRUNCATE finance_personal_audit,finance_classifications,finance_classification_rules,finance_profile,pluggy_jobs,pluggy_inbox,pluggy_snapshots,pluggy_audit,pluggy_accounts,pluggy_transactions,pluggy_investments,pluggy_items CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
	item := memory.ID()
	_, err = s.DB.Exec(ctx, `INSERT INTO pluggy_items(item_id,user_id,status,explicit_owner,last_synced_at,coverage) VALUES($1,$2,'connected',true,$3,'{"complete_for_reported_products":true}');`, item, memory.UserID, at)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO pluggy_accounts(user_id,item_id,external_id,name,account_type,subtype,balance,currency,observed_at,content_hash,payload) VALUES($1,$2,'checking','Conta','BANK','CHECKING_ACCOUNT',10000,'BRL',$3,'a','{}')`, memory.UserID, item, at)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO pluggy_transactions(user_id,item_id,external_id,account_external_id,description,amount,currency,effective_at,provider_status,observed_at,content_hash,payload) SELECT $1,$2,'expense-'||n,'checking','Mercado',1,'BRL','2026-11-12','POSTED',$3,'a','{"type":"DEBIT","category":"Groceries"}' FROM generate_series(1,650)n`, memory.UserID, item, at)
	if err != nil {
		t.Fatal(err)
	}
	p, err := ReadPersonalFinance(ctx, s.DB, memory.UserID, at)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Expenses) != 1 || p.Expenses[0].AmountCents != 65000 || p.Expenses[0].Count != 650 {
		t.Fatalf("truncated totals: %+v", p.Expenses)
	}
	if p.AvailableToInvestCents != nil || p.ProfileComplete || p.CanRecommend {
		t.Fatal("missing profile accepted")
	}
	saved, err := s.SaveProfile(ctx, memory.UserID, completeProfile())
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version != 1 {
		t.Fatal(saved)
	}
	if _, err = s.SaveProfile(ctx, memory.UserID, completeProfile()); !errors.Is(err, ErrConflict) {
		t.Fatal("profile conflict lost", err)
	}
	if err = s.SaveClassification(ctx, memory.UserID, ClassificationInput{TransactionID: "expense-1", Category: "Mercado corrigido", Kind: "expense", ApplyRule: true}); err != nil {
		t.Fatal(err)
	}
	p, err = ReadPersonalFinance(ctx, s.DB, memory.UserID, at)
	if err != nil {
		t.Fatal(err)
	}
	before := p.Revision
	if !p.CanRecommend || p.AvailableToInvestCents == nil || *p.AvailableToInvestCents != 1000000 {
		t.Fatalf("complete financial context: %+v", p)
	}
	_, err = s.DB.Exec(ctx, `UPDATE pluggy_items SET last_synced_at=last_synced_at+interval '1 minute'; UPDATE pluggy_transactions SET version=version+1,content_hash='changed',observed_at=observed_at+interval '1 minute'`)
	if err != nil {
		t.Fatal(err)
	}
	p, err = ReadPersonalFinance(ctx, s.DB, memory.UserID, at.Add(time.Minute))
	if err != nil || p.Revision != before {
		t.Fatal("identical ingestion changed semantic revision", err)
	}
	for _, dates := range [][2]*time.Time{{ptrTime(at), ptrTime(at.Add(-48 * time.Hour))}, {ptrTime(at.Add(-48 * time.Hour)), ptrTime(at)}} {
		if _, err = s.DB.Exec(ctx, `UPDATE pluggy_items SET last_synced_at=$1,last_source_sync=$2`, dates[0], dates[1]); err != nil {
			t.Fatal(err)
		}
		stale, err := ReadPersonalFinance(ctx, s.DB, memory.UserID, at)
		if err != nil || stale.State != "stale" || stale.CanRecommend || stale.AvailableToInvestCents != nil || stale.Revision == before {
			t.Fatalf("cached or old local data became fresh: state=%s recommend=%v err=%v", stale.State, stale.CanRecommend, err)
		}
	}
	if _, err = s.DB.Exec(ctx, `UPDATE pluggy_items SET last_synced_at=$1,last_source_sync=NULL`, at); err != nil {
		t.Fatal(err)
	}
	p, err = ReadPersonalFinance(ctx, s.DB, memory.UserID, at)
	if err != nil || !p.CanRecommend || p.Revision != before {
		t.Fatalf("missing provider timestamp lost local fallback: %+v %v", p, err)
	}
	txs, err := ReadBankTransactions(ctx, s.DB, memory.UserID, at)
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 650 || txs[0].CategoryName != "Mercado corrigido" {
		t.Fatal("rule missing", len(txs))
	}
	_, err = s.DB.Exec(ctx, `UPDATE pluggy_items SET status='revoked'`)
	if err != nil {
		t.Fatal(err)
	}
	p, err = ReadPersonalFinance(ctx, s.DB, memory.UserID, at)
	if err != nil || p.Revision == before || p.CanRecommend || p.AvailableToInvestCents != nil {
		t.Fatal("revocation did not gate cash", err)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestBankTotalsSeparateCurrenciesPendingRefundsAndMovements(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	_, err := s.DB.Exec(ctx, `TRUNCATE finance_personal_audit,finance_classifications,finance_classification_rules,finance_profile,pluggy_jobs,pluggy_inbox,pluggy_snapshots,pluggy_audit,pluggy_accounts,pluggy_transactions,pluggy_investments,pluggy_items CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
	item := memory.ID()
	_, err = s.DB.Exec(ctx, `INSERT INTO pluggy_items(item_id,user_id,status,explicit_owner,last_synced_at,coverage) VALUES($1,$2,'connected',true,$3,'{"complete_for_reported_products":true}')`, item, memory.UserID, at)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO pluggy_accounts(user_id,item_id,external_id,account_type,subtype,balance,currency,observed_at,content_hash,payload) VALUES($1,$2,'account','BANK','CHECKING_ACCOUNT',1000,'BRL',$3,'x','{}')`, memory.UserID, item, at)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct{ id, category, direction, amount, currency, status string }{
		{"purchase", "Groceries", "DEBIT", "10", "BRL", "POSTED"},
		{"same-merchant", "Groceries", "DEBIT", "20", "BRL", "POSTED"},
		{"refund", "Refund", "CREDIT", "2", "BRL", "POSTED"},
		{"bill", "Credit card payment", "DEBIT", "10", "BRL", "POSTED"},
		{"transfer", "Same person transfer - PIX", "DEBIT", "10", "BRL", "POSTED"},
		{"application", "Fixed income", "DEBIT", "10", "BRL", "POSTED"},
		{"pending", "Groceries", "DEBIT", "3", "BRL", "PENDING"},
		{"foreign", "Groceries", "DEBIT", "4", "USD", "POSTED"},
	} {
		_, err = s.DB.Exec(ctx, `INSERT INTO pluggy_transactions(user_id,item_id,external_id,account_external_id,description,amount,currency,effective_at,provider_status,observed_at,content_hash,payload) VALUES($1,$2,$3,'account','Mesmo comércio',$4::text::numeric,$5,'2026-11-12',$6,$7,'x',jsonb_build_object('category',$8::text,'type',$9::text))`, memory.UserID, item, v.id, v.amount, v.currency, v.status, at, v.category, v.direction)
		if err != nil {
			t.Fatal(err)
		}
	}
	p, err := ReadPersonalFinance(ctx, s.DB, memory.UserID, at)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Expenses) != 2 || p.Expenses[0].Currency != "BRL" || p.Expenses[0].AmountCents != 2800 || p.Expenses[1].AmountCents != 400 || len(p.Pending) != 1 || p.Pending[0].AmountCents != 300 {
		t.Fatalf("invalid totals: %+v %+v", p.Expenses, p.Pending)
	}
	if err = s.SaveClassification(ctx, memory.UserID, ClassificationInput{TransactionID: "purchase", Category: "Alimentação corrigida", Kind: "expense", ApplyRule: true}); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveClassification(ctx, memory.UserID, ClassificationInput{TransactionID: "purchase", Category: "Exceção", Kind: "transfer_internal", Version: 1}); err != nil {
		t.Fatal(err)
	}
	rows, err := ReadBankTransactions(ctx, s.DB, memory.UserID, at)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == "purchase" && (row.Kind != "transfer_internal" || row.ClassificationSource != "user") {
			t.Fatal("individual correction lost precedence", row)
		}
		if row.ID == "same-merchant" && (row.CategoryName != "Alimentação corrigida" || row.ClassificationSource != "rule") {
			t.Fatal("rule not applied", row)
		}
		if row.ID == "refund" && row.Kind != "refund" {
			t.Fatal("expense rule overrode a credit refund", row)
		}
	}
	if err = s.SaveClassification(ctx, otherUser, ClassificationInput{TransactionID: "purchase", Category: "Forbidden", Kind: "expense"}); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-user correction", err)
	}
	past := completeProfile()
	past.Goals[0].TargetDate = "2026-10-01"
	if _, err = s.SaveProfile(ctx, memory.UserID, past); err != nil {
		t.Fatal(err)
	}
	p, err = ReadPersonalFinance(ctx, s.DB, memory.UserID, at)
	if err != nil || p.CanRecommend || p.ProfileComplete {
		t.Fatal("expired goal accepted", err)
	}
}
