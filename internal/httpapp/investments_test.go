package httpapp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"openchris/conselho/internal/finance"
	"openchris/conselho/internal/memory"
	"openchris/conselho/internal/pluggy"
)

func dashboardInvestments(t *testing.T) (*App, *httptest.Server, []*http.Cookie, string) {
	t.Helper()
	store, _, cookies, _ := productionFixture(t)
	if _, err := store.DB.Exec(context.Background(), `TRUNCATE pluggy_items CASCADE`); err != nil {
		t.Fatal(err)
	}
	item := memory.ID()
	_, err := store.DB.Exec(context.Background(), `INSERT INTO pluggy_items(item_id,user_id,status,explicit_owner,consent_expires_at,last_synced_at,coverage) VALUES($1,$2,'connected',true,'2099-01-01',clock_timestamp(),'{"source_products":["INVESTMENTS"],"complete_for_reported_products":true}')`, item, memory.UserID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.DB.Exec(context.Background(), `INSERT INTO pluggy_investments(user_id,item_id,external_id,name,investment_type,balance,currency,observed_at,content_hash,payload) VALUES($1,$2,'active','Título <script>unsafe</script>','FIXED_INCOME',123.45,'BRL',clock_timestamp(),'fixture','{"status":"ACTIVE"}'),($1,$2,'withdrawn','Resgatado','FIXED_INCOME',999,'BRL',clock_timestamp(),'fixture','{"status":"TOTAL_WITHDRAWAL"}')`, memory.UserID, item)
	if err != nil {
		t.Fatal(err)
	}
	_, err = finance.New(store.DB).SaveNetWorth(context.Background(), memory.UserID, finance.NetWorthInput{AssetsCents: 10000, LiabilitiesCents: 0, Date: financeToday(), IdempotencyKey: memory.ID()})
	if err != nil {
		t.Fatal(err)
	}
	app := New(store, Config{Production: true, Password: "production-test-password-1234", PublicURL: "https://chris.example.test", Pluggy: pluggy.Config{Enabled: true, ClientID: "synthetic", ClientSecret: "synthetic", WebhookSecret: strings.Repeat("s", 32), PublicURL: "https://chris.example.test/webhooks/pluggy", HTTPClient: &http.Client{Transport: denyProviderReads{t}}}})
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	return app, server, cookies, item
}

type denyProviderReads struct{ t *testing.T }

func (d denyProviderReads) RoundTrip(*http.Request) (*http.Response, error) {
	d.t.Fatal("dashboard read called Pluggy provider")
	return nil, nil
}

func TestInvestmentDashboardSummaryAndInitialHTMLRemainSeparate(t *testing.T) {
	_, server, cookies, _ := dashboardInvestments(t)
	unauthorized := call(t, server, "GET", "/api/finance/summary", "", nil, "")
	if unauthorized.StatusCode != 401 {
		t.Fatal(unauthorized.StatusCode)
	}
	response := call(t, server, "GET", "/api/finance/summary", "", cookies, "")
	var summary FinanceSummary
	if err := json.NewDecoder(response.Body).Decode(&summary); err != nil {
		t.Fatal(err)
	}
	if summary.Investments.ActiveCount != 1 || summary.Investments.WithdrawnCount != 1 || summary.Investments.Totals[0].Balance == nil || *summary.Investments.Totals[0].Balance != "123.45" || summary.NetWorthCents == nil || *summary.NetWorthCents != 10000 {
		t.Fatal("merged manual wealth or omitted imports", summary)
	}
	if bank := summary.PluggyWealth; bank == nil || bank.Net != "R$ 123,45" || bank.Status != "Parcial" || len(bank.Issues) == 0 {
		t.Fatalf("bank wealth and coverage: %+v", bank)
	}
	page := call(t, server, "GET", "/dashboard", "", cookies, "")
	body, err := io.ReadAll(page.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`id="investment-panel"`, "Investimentos via Pluggy", `id="live-net-worth">R$ 123,45`, `id="live-manual-wealth">R$ 100,00`, "Ver dados que faltam", `id="investment-composition"`, `Título &lt;script&gt;unsafe&lt;/script&gt;`, `</html>`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("initial HTML missing %q", want)
		}
	}
	if strings.Contains(string(body), "Título <script>") {
		t.Fatal("unsafe provider HTML")
	}
}

func TestInvestmentDashboardRefreshesSnapshotWithoutChangingManualWealth(t *testing.T) {
	app, server, cookies, item := dashboardInvestments(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/finance/events", nil)
	req.AddCookie(cookies[0])
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	before := readFinanceSnapshot(t, reader)
	if before.Investments.ActiveCount != 1 {
		t.Fatal("stream omitted investments")
	}
	if _, err = app.store.DB.Exec(ctx, `UPDATE pluggy_investments SET balance=200,version=version+1 WHERE item_id=$1 AND external_id='active'`, item); err != nil {
		t.Fatal(err)
	}
	after := readFinanceSnapshot(t, reader)
	if after.Investments.Totals[0].Balance == nil || *after.Investments.Totals[0].Balance != "200" || after.Investments.Revision == before.Investments.Revision || *after.NetWorthCents != 10000 {
		t.Fatal("stream not refreshed or manual wealth changed", after)
	}
}
