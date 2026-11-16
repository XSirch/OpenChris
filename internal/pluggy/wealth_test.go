package pluggy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"openchris/conselho/internal/memory"
)

func TestWealthCountsAuthorizedPositionsOnceAndExplainsGaps(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	recent, old, expired := portfolioSource(t, db), portfolioSource(t, db), portfolioSource(t, db)
	base := now.Add(-2 * time.Hour)
	older := now.Add(-48 * time.Hour)
	_, err := db.Exec(ctx, `UPDATE pluggy_items SET last_source_sync=CASE WHEN item_id=$1 THEN $4::timestamptz ELSE $5::timestamptz END,
	  consent_expires_at=CASE WHEN item_id=$3 THEN $5 ELSE '2099-01-01' END,
	  wealth_loans=CASE WHEN item_id=$1 THEN '[{"key":"loan-1","kind":"LOAN","currency":"BRL","balance":"300.15"}]'::jsonb END
	  WHERE item_id IN ($1,$2,$3)`, recent, old, expired, base, older)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		item, id, kind, amount, payload string
	}{
		{recent, "checking", "BANK", "1000.25", `{"bankData":{"transferNumber":"001/1234/12345-6","automaticallyInvestedBalance":1000.25}}`},
		{old, "old-checking", "BANK", "1000.00", `{"bankData":{"transferNumber":"001/1234/12345-6"}}`},
		{recent, "overdraft", "BANK", "-50", `{}`},
		{recent, "card", "CREDIT", "75.50", `{"creditData":{"holderType":"MAIN","creditLimit":10000}}`},
		{recent, "add-on", "CREDIT", "75.50", `{"creditData":{"holderType":"ADDITIONAL"}}`},
		{expired, "unconsented", "BANK", "99999", `{}`},
	} {
		_, err = db.Exec(ctx, `INSERT INTO pluggy_accounts(user_id,item_id,external_id,account_type,subtype,balance,currency,observed_at,content_hash,payload)
	      VALUES($1,$2,$3,$4,'CHECKING_ACCOUNT',$5::text::numeric,'BRL',clock_timestamp(),'fixture',$6::jsonb)`, memory.UserID, row.item, row.id, row.kind, row.amount, row.payload)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		item, id, currency, amount, payload string
	}{
		{recent, "fund", "BRL", "200.555", `{"status":"ACTIVE","isin":"FUND1"}`},
		{old, "consolidated", "BRL", "200.555", `{"status":"ACTIVE","isin":"FUND1","institution":{"number":"123"}}`},
		{old, "withdrawn", "BRL", "9999", `{"status":"TOTAL_WITHDRAWAL"}`},
		{old, "foreign", "USD", "222", `{"status":"ACTIVE"}`},
	} {
		_, err = db.Exec(ctx, `INSERT INTO pluggy_investments(user_id,item_id,external_id,balance,currency,observed_at,content_hash,payload)
	      VALUES($1,$2,$3,$4::text::numeric,$5,clock_timestamp(),'fixture',$6::jsonb)`, memory.UserID, row.item, row.id, row.amount, row.currency, row.payload)
		if err != nil {
			t.Fatal(err)
		}
	}
	result, err := readWealth(ctx, db, now)
	if err != nil || result.Net != "R$ 775,16" || result.Cash != "R$ 1.000,25" || result.Investments != "R$ 200,56" || result.Debt != "R$ 425,65" ||
		result.BankAsOf.Sub(older).Abs() > time.Millisecond || result.LastImported.IsZero() {
		t.Fatalf("wealth: %+v, %v", result, err)
	}
	for _, gap := range []string{"autorização", "desatualizados", "dívidas", "outras moedas", "sobreposição"} {
		if !strings.Contains(strings.Join(result.Issues, " "), gap) {
			t.Errorf("missing coverage distinction %q: %v", gap, result.Issues)
		}
	}
}

func TestWealthLoanReadUsesOutstandingAndDistinguishesEmptyFromMissing(t *testing.T) {
	id := memory.ID()
	mode := "loan"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/auth":
			fmt.Fprint(w, `{"apiKey":"local-fixture"}`)
		case r.URL.Path != "/loans" || r.URL.Query().Get("itemId") != id:
			t.Error("unexpected provider request", r.Method, r.URL.Path)
			http.NotFound(w, r)
		case mode == "loan":
			fmt.Fprintf(w, `{"page":1,"totalPages":1,"results":[{"id":%q,"itemId":%q,"ipocCode":"test-contract","kind":"FINANCING","currencyCode":"BRL","contractAmount":9000,"payments":{"contractOutstandingBalance":120.15}}]}`, memory.ID(), id)
		case mode == "empty":
			fmt.Fprint(w, `{"page":0,"totalPages":0,"results":[]}`)
		default:
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	s := Service{client: NewClient("fixture-id", "fixture-secret", WithBaseURL(server.URL), WithHTTPClient(server.Client()))}
	products := Object{"products": []any{"LOANS"}}
	if loans := s.readWealthLoans(context.Background(), id, products); len(loans) != 1 || loans[0].Balance != "120.15" || loans[0].Kind != "FINANCING" || strings.Contains(loans[0].Key, "test-contract") {
		t.Fatalf("loan: %+v", loans)
	}
	mode = "empty"
	if loans := s.readWealthLoans(context.Background(), id, products); loans == nil || len(loans) != 0 {
		t.Fatalf("empty response is confirmed: %+v", loans)
	}
	mode = "fail"
	if loans := s.readWealthLoans(context.Background(), id, products); loans != nil {
		t.Fatalf("missing coverage: %+v", loans)
	}
}
