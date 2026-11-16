package httpapp

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"openchris/conselho/internal/finance"
	"openchris/conselho/internal/memory"
	"strings"
	"testing"
)

func TestBankExpenseReportKeepsCurrenciesSeparate(t *testing.T) {
	var rows []ExpenseTransaction
	if err := json.Unmarshal([]byte(`[{"id":"brl","date":"2026-11-10","category":"food","kind":"expense","status":"confirmed","amount_cents":1200,"currency":"BRL"},{"id":"usd","date":"2026-11-10","category":"food","kind":"expense","status":"confirmed","amount_cents":900,"currency":"USD"}]`), &rows); err != nil {
		t.Fatal(err)
	}
	options := []ExpenseCategory{{ID: "food", Name: "Alimentação"}}
	report, err := buildExpenseReportFrom(url.Values{"month": {"2026-11"}, "currency": {"BRL"}}, rows, options, "bank")
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalCents == nil || *report.TotalCents != 1200 || report.ResultCount != 1 {
		t.Fatalf("currencies merged: %+v", report)
	}
	other, err := buildExpenseReportFrom(url.Values{"month": {"2026-11"}, "currency": {"USD"}}, rows, options, "bank")
	if err != nil || other.TotalCents == nil || *other.TotalCents != 900 || strings.Contains(other.TotalText, "R$") {
		t.Fatalf("foreign currency mislabeled: %+v, %v", other, err)
	}
	if !strings.Contains(other.ExportURL, "currency=USD") {
		t.Fatal("export lost currency")
	}
}

func TestPersonalProfileWritesAreValidatedVersionedAndDoNotWriteConversation(t *testing.T) {
	store, server, cookies, csrf := productionFixture(t)
	ctx := context.Background()
	if _, err := store.DB.Exec(ctx, `TRUNCATE finance_profile,finance_personal_audit`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DB.Exec(ctx, `TRUNCATE finance_profile,finance_personal_audit`) })
	input := `{"version":0,"confirmed":false,"goals":[],"risk_profile":"conservative","commitments_30_days_cents":null}`
	if got := call(t, server, "POST", "/api/finance/profile", input, cookies, ""); got.StatusCode != 403 {
		t.Fatal("missing CSRF accepted", got.StatusCode)
	}
	for _, bad := range []string{`{"risk_profile":"unlimited"}`, `{"minimum_checking_cents":-1}`, `{"unknown_field":true}`, `{} {}`} {
		if got := call(t, server, "POST", "/api/finance/profile", bad, cookies, csrf); got.StatusCode != 400 {
			t.Fatalf("invalid input %s: %d", bad, got.StatusCode)
		}
	}
	response := call(t, server, "POST", "/api/finance/profile", input, cookies, csrf)
	var saved finance.Profile
	if response.StatusCode != 200 {
		b, _ := io.ReadAll(response.Body)
		t.Fatalf("save %d %s", response.StatusCode, b)
	}
	if err := json.NewDecoder(response.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	if saved.Version != 1 || saved.Commitments30DaysCents != nil || saved.Confirmed {
		t.Fatal("unknown or confirmation overwritten", saved)
	}
	if got := call(t, server, "POST", "/api/finance/profile", input, cookies, csrf); got.StatusCode != 409 {
		t.Fatal("stale profile overwrote changes", got.StatusCode)
	}
	var turns int
	if err := store.DB.QueryRow(ctx, `SELECT count(*) FROM conversation_events`).Scan(&turns); err != nil || turns != 0 {
		t.Fatal("profile created a conversation", turns, err)
	}
}

func TestBankRowsReachReportCSVSummaryAndActionableProfile(t *testing.T) {
	store, server, cookies, csrf := productionFixture(t)
	ctx := context.Background()
	cleanup := func() {
		store.DB.Exec(ctx, `TRUNCATE finance_profile,finance_personal_audit,finance_classification_rules,pluggy_items CASCADE`)
	}
	cleanup()
	t.Cleanup(cleanup)
	item := memory.ID()
	if _, err := store.DB.Exec(ctx, `INSERT INTO pluggy_items(item_id,user_id,status,explicit_owner,last_synced_at,coverage) VALUES($1,$2,'connected',true,clock_timestamp(),'{"complete_for_reported_products":true}')`, item, memory.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(ctx, `INSERT INTO pluggy_accounts(user_id,item_id,external_id,name,account_type,subtype,balance,currency,observed_at,content_hash,payload) VALUES($1,$2,'checking','Conta sintética','BANK','CHECKING_ACCOUNT',10000,'BRL',clock_timestamp(),'fixture','{}')`, memory.UserID, item); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(ctx, `INSERT INTO pluggy_transactions(user_id,item_id,external_id,account_external_id,description,amount,currency,effective_at,provider_status,observed_at,content_hash,payload) SELECT $1,$2,'bank-'||n,'checking','fixture-private-description',1.20,'BRL',$3,'POSTED',clock_timestamp(),'fixture','{"type":"DEBIT","category":"Groceries"}' FROM generate_series(1,501)n`, memory.UserID, item, financeToday()); err != nil {
		t.Fatal(err)
	}
	response := call(t, server, "GET", "/api/expenses", "", cookies, "")
	var report ExpenseReport
	if err := json.NewDecoder(response.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	if report.Mode != "bank" || report.TotalCents == nil || *report.TotalCents != 60120 || len(report.Transactions) != 501 {
		t.Fatalf("bank report incomplete: mode=%s rows=%d total=%v", report.Mode, len(report.Transactions), report.TotalCents)
	}
	manual := call(t, server, "GET", "/api/expenses?source=manual", "", cookies, "")
	var manualReport ExpenseReport
	json.NewDecoder(manual.Body).Decode(&manualReport)
	if manualReport.HasData {
		t.Fatal("bank transactions copied into manual ledger")
	}
	export := call(t, server, "GET", report.ExportURL, "", cookies, "")
	csvData, _ := io.ReadAll(export.Body)
	if strings.Count(string(csvData), "fixture-private-description") != 501 {
		t.Fatal("CSV truncated imported rows")
	}
	summaryResponse := call(t, server, "GET", "/api/finance/summary", "", cookies, "")
	var summary FinanceSummary
	if err := json.NewDecoder(summaryResponse.Body).Decode(&summary); err != nil {
		t.Fatal(err)
	}
	if summary.ExpenseCents != 60120 || summary.ExpenseCount != 501 || summary.AvailableToInvestCents != nil || len(summary.FinancePendingItems) == 0 {
		t.Fatal("summary inconsistent or missing data treated as zero", summary)
	}
	encoded, _ := json.Marshal(summary)
	if strings.Contains(string(encoded), "fixture-private-description") || strings.Contains(string(encoded), `"checking"`) {
		t.Fatal("summary leaked raw bank data")
	}
	classification := `{"transaction_id":"bank-1","category":"Transferência própria","kind":"transfer_internal","apply_rule":false,"version":0}`
	if got := call(t, server, "POST", "/api/finance/classifications", classification, cookies, csrf); got.StatusCode != 200 {
		b, _ := io.ReadAll(got.Body)
		t.Fatalf("classify %d %s", got.StatusCode, b)
	}
	after := call(t, server, "GET", "/api/finance/summary", "", cookies, "")
	json.NewDecoder(after.Body).Decode(&summary)
	if summary.ExpenseCents != 60000 || summary.ExpenseCount != 500 {
		t.Fatal("classification did not update summary", summary.ExpenseCents, summary.ExpenseCount)
	}
	if _, err := store.DB.Exec(ctx, `UPDATE pluggy_transactions SET amount=NULL WHERE user_id=$1 AND external_id<>'bank-1'`, memory.UserID); err != nil {
		t.Fatal(err)
	}
	unknownSummary := call(t, server, "GET", "/api/finance/summary", "", cookies, "")
	if err := json.NewDecoder(unknownSummary.Body).Decode(&summary); err != nil {
		t.Fatal(err)
	}
	if summary.ExpenseText != "Dados incompletos" || summary.ExpenseCount != 0 {
		t.Fatalf("unknown amounts became zero expenses: %+v", summary)
	}
	if _, err := store.DB.Exec(ctx, `UPDATE pluggy_transactions SET amount=12.34,currency='',description='missing currency row' WHERE user_id=$1 AND external_id='bank-2'`, memory.UserID); err != nil {
		t.Fatal(err)
	}
	unknownCSV := call(t, server, "GET", "/expenses.csv?source=bank&review=1", "", cookies, "")
	reader := csv.NewReader(unknownCSV.Body)
	reader.Comma = ';'
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range records {
		if record[1] == "missing currency row" {
			found = true
			if record[6] != "" {
				t.Fatalf("CSV assigned BRL to unknown currency: %v", record)
			}
		}
	}
	if !found {
		t.Fatal("CSV review hid missing currency row")
	}
}

func TestReviewFindsUnknownRowsOutsideSelectedMonthWithoutInventingAmount(t *testing.T) {
	rows := []ExpenseTransaction{{ID: "old", Date: "2026-01-02", Currency: "BRL", Kind: "unknown", ReviewRequired: true, AmountUnknown: true, Category: "unknown"}}
	r, err := buildExpenseReportFrom(url.Values{"source": {"bank"}, "month": {"2026-11"}, "review": {"1"}}, rows, []ExpenseCategory{{ID: "unknown", Name: "Sem categoria"}}, "bank")
	if err != nil {
		t.Fatal(err)
	}
	if r.ResultCount != 1 || r.ResultCents != 0 || r.TotalCents != nil || r.Transactions[0].AmountText != "Não informado" {
		t.Fatal("review hid historical gap or invented zero", r)
	}
}

func TestBankReportKeepsNonExpensesVisibleForClassification(t *testing.T) {
	var rows []ExpenseTransaction
	_ = json.Unmarshal([]byte(`[{"id":"expense","date":"2026-11-10","category":"food","kind":"expense","status":"confirmed","amount_cents":1200,"currency":"BRL"},{"id":"review","date":"2026-11-10","category":"unknown","kind":"unknown","status":"confirmed","amount_cents":900,"currency":"BRL","review_required":true},{"id":"transfer","date":"2026-11-10","kind":"transfer_internal","status":"confirmed","amount_cents":50000,"currency":"BRL"}]`), &rows)
	report, err := buildExpenseReportFrom(url.Values{"month": {"2026-11"}}, rows, []ExpenseCategory{{ID: "food", Name: "Alimentação"}, {ID: "unknown", Name: "Sem categoria"}}, "bank")
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalCents == nil || *report.TotalCents != 1200 || report.ResultCents != 1200 || report.ResultCount != 3 {
		t.Fatalf("classification rows hidden or counted as spending: %+v", report)
	}
}

func TestBankReportDistinguishesIncompleteTotalsFromZero(t *testing.T) {
	known := ExpenseTransaction{ID: "known", Date: "2026-11-10", Currency: "BRL", Kind: "expense", Status: "confirmed", AmountCents: 1200}
	unknown := ExpenseTransaction{ID: "unknown", Date: "2026-11-11", Currency: "BRL", Kind: "expense", Status: "confirmed", AmountUnknown: true, ReviewRequired: true}
	for _, tc := range []struct {
		name      string
		rows      []ExpenseTransaction
		wantTotal *int64
		wantText  string
	}{
		{name: "unknown amount", rows: []ExpenseTransaction{unknown}, wantText: "Dados incompletos"},
		{name: "known subtotal", rows: []ExpenseTransaction{known, unknown}, wantTotal: &known.AmountCents, wantText: "R$ 12,00 (parcial)"},
		{name: "unknown nature", rows: []ExpenseTransaction{{ID: "nature", Date: "2026-11-11", Currency: "BRL", Kind: "unknown", Status: "confirmed", AmountCents: 500, ReviewRequired: true}}, wantText: "Dados incompletos"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := buildExpenseReportFrom(url.Values{"month": {"2026-11"}}, tc.rows, nil, "bank")
			if err != nil {
				t.Fatal(err)
			}
			if (r.TotalCents == nil) != (tc.wantTotal == nil) || r.TotalCents != nil && *r.TotalCents != *tc.wantTotal || r.TotalText != tc.wantText {
				t.Fatalf("incomplete spending presented as complete: cents=%v text=%q", r.TotalCents, r.TotalText)
			}
			if len(r.Months) != 1 || r.Months[0].TotalText != tc.wantText {
				t.Fatalf("month selector hid incomplete data: %+v", r.Months)
			}
		})
	}
	unknown.Date = "2026-10-11"
	r, err := buildExpenseReportFrom(url.Values{"month": {"2026-11"}}, []ExpenseTransaction{known, unknown}, nil, "bank")
	if err != nil {
		t.Fatal(err)
	}
	if r.PreviousTotalCents != nil || r.ComparisonText != "Há dados incompletos. Revise os lançamentos antes de comparar os meses." {
		t.Fatalf("unknown previous spending became a zero comparison: %+v", r)
	}
}

func TestReviewNeverAssignsSelectedCurrencyToMissingCurrency(t *testing.T) {
	row := ExpenseTransaction{ID: "missing-currency", Date: "2026-11-10", Kind: "expense", Status: "confirmed", AmountCents: 1200, ReviewRequired: true}
	r, err := buildExpenseReportFrom(url.Values{"month": {"2026-11"}, "review": {"1"}}, []ExpenseTransaction{row}, nil, "bank")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Transactions) != 1 || r.Transactions[0].AmountText != "Moeda não informada" || r.TotalCents != nil {
		t.Fatalf("missing currency became BRL: %+v", r)
	}
}

func TestPersonalFinanceRoutesRequireAuthentication(t *testing.T) {
	app := New(nil, Config{Demo: true, Password: "demo-conselho", TelegramSecret: "synthetic"})
	for _, route := range []struct{ method, path string }{{"GET", "/api/finance/personal"}, {"POST", "/api/finance/profile"}, {"POST", "/api/finance/classifications"}} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`)))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: %d", route.method, route.path, w.Code)
		}
	}
}

func TestReadinessRequiresPersonalFinanceMigrations(t *testing.T) {
	s, server, _, _ := productionFixture(t)
	ctx := context.Background()
	for _, version := range []int{8, 9} {
		if _, err := s.DB.Exec(ctx, `DELETE FROM schema_migrations WHERE version=$1`, version); err != nil {
			t.Fatal(err)
		}
		out := call(t, server, "GET", "/readyz", "", nil, "")
		_, err := s.DB.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, version)
		if err != nil {
			t.Fatal(err)
		}
		out.Body.Close()
		if out.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("readiness accepted missing migration %d: %d", version, out.StatusCode)
		}
	}
}

func TestActionableFinanceLoginTargets(t *testing.T) {
	for _, target := range []string{"/finance/profile", "/finance/profile#risk-profile", "/finance/profile#commitments-30-days-cents", "/reports#report-reading"} {
		if got := safeReturnTarget(target); got != target {
			t.Errorf("lost action %q -> %q", target, got)
		}
	}
	if safeReturnTarget("/finance/profile?next=https://evil.test") != "/dashboard" {
		t.Fatal("unsafe profile return accepted")
	}
}
