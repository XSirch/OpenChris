package httpapp

import (
	"context"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"openchris/conselho/internal/finance"
	"openchris/conselho/internal/memory"
)

func productionFixture(t *testing.T) (*memory.Store, *httptest.Server, []*http.Cookie, string) {
	t.Helper()
	s, _ := manualFixture(t)
	cfg := Config{Production: true, Password: "production-test-password-1234", PublicURL: "https://chris.example.test", SessionTTL: time.Hour}
	server := httptest.NewServer(New(s, cfg))
	t.Cleanup(server.Close)
	token, csrf := randomToken(), randomToken()
	if _, err := s.DB.Exec(context.Background(), `INSERT INTO sessions(token_hash,csrf,expires_at) VALUES($1,$2,now()+interval '1 hour')`, digest(token), csrf); err != nil {
		t.Fatal(err)
	}
	return s, server, []*http.Cookie{{Name: "conselho_session", Value: token}}, csrf
}

func TestProductionConfigAllowsManualCoreWithoutIntegrationSecrets(t *testing.T) {
	good := Config{Production: true, Password: "production-test-password-1234", PublicURL: "https://chris.example.test"}
	if err := good.Validate(); err != nil {
		t.Fatalf("disabled optional integration blocked manual core: %v", err)
	}
	for _, cfg := range []Config{
		{Production: true, Demo: true, Password: good.Password, PublicURL: good.PublicURL},
		{Production: true, Password: good.Password},
		{Production: true, Password: "demo-conselho", PublicURL: good.PublicURL},
		{Production: true, Password: good.Password, PublicURL: "http://chris.example.test"},
		{Production: true, Password: good.Password, PublicURL: "https://:443"},
	} {
		if err := cfg.Validate(); err == nil {
			t.Fatalf("unsafe production config accepted: %+v", cfg)
		}
	}
}

func TestProductionDisablesSyntheticRoutesWithoutPersistingTurns(t *testing.T) {
	s, server, cookies, csrf := productionFixture(t)
	for _, path := range []string{"/demo/telegram", "/webhooks/telegram", "/bench/query", "/bench/webhook", "/bench/events", "/bench/dashboard", "/static/benchmark.html", "/static/reference.png"} {
		method := http.MethodGet
		if path == "/demo/telegram" || path == "/webhooks/telegram" || path == "/bench/webhook" {
			method = http.MethodPost
		}
		res := call(t, server, method, path, `{"text":"Olá","idempotency_key":"production-test-turn"}`, cookies, csrf)
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("synthetic route %s exposed: %d", path, res.StatusCode)
		}
	}
	for _, tc := range []struct {
		cookies []*http.Cookie
		csrf    string
		want    int
	}{{nil, csrf, 401}, {cookies, "", 403}, {cookies, csrf, 503}} {
		res := call(t, server, "POST", "/api/conversations/turn", `{"text":"Olá","idempotency_key":"production-test-turn"}`, tc.cookies, tc.csrf)
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != tc.want || (tc.want == 503 && !strings.Contains(string(body), `"code":"integration_not_configured"`)) {
			t.Fatalf("turn: %d %s", res.StatusCode, body)
		}
	}
	for _, table := range []string{"conversation_events", "jobs", "stream_events", "episodes", "memory_facts", "context_manifests"} {
		var count int
		if err := s.DB.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("disabled integration wrote %s: count=%d err=%v", table, count, err)
		}
	}
	res := call(t, server, "GET", "/api/state", "", cookies, "")
	var state struct {
		Mode         string            `json:"mode"`
		Processor    string            `json:"processor"`
		Capabilities map[string]string `json:"capabilities"`
	}
	if err := json.NewDecoder(res.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.Mode != "production" || state.Processor != "disabled" || state.Capabilities["finance"] != "manual" || state.Capabilities["models"] != "not_configured" || state.Capabilities["telegram"] != "not_configured" || state.Capabilities["banks"] != "not_configured" {
		t.Fatalf("misleading runtime state: %+v", state)
	}
}

func TestProductionRejectsDemoSourceInEveryExpenseRepresentation(t *testing.T) {
	_, server, cookies, _ := productionFixture(t)
	for _, path := range []string{"/expenses?source=demo", "/api/expenses?source=demo", "/expenses.csv?source=demo", "/api/expenses?source=manual&source=demo"} {
		res := call(t, server, "GET", path, "", cookies, "")
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != http.StatusBadRequest || strings.Contains(string(body), "Aluguel do apartamento") || strings.Contains(string(body), "552.450") {
			t.Fatalf("demo source %s: %d %s", path, res.StatusCode, body)
		}
	}
}

func TestProductionOverviewOnlyUsesManuallyRecordedNetWorth(t *testing.T) {
	s, server, cookies, _ := productionFixture(t)
	for _, withWealth := range []bool{false, true} {
		if withWealth {
			_, err := finance.New(s.DB).SaveNetWorth(context.Background(), memory.UserID, finance.NetWorthInput{AssetsCents: 10000, LiabilitiesCents: 12500, Date: "2026-10-27", IdempotencyKey: "production-wealth-test"})
			if err != nil {
				t.Fatal(err)
			}
		}
		res := call(t, server, "GET", "/api/overview", "", cookies, "")
		var out map[string]any
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != 200 || out["mode"] != "manual" || out["available_cents"] != nil || out["reserve_months"] != nil || len(out["series"].([]any)) != 0 || len(out["goals"].([]any)) != 0 {
			t.Fatalf("invented production overview: %+v", out)
		}
		if !withWealth && out["net_worth_cents"] != nil || withWealth && out["net_worth_cents"] != float64(-2500) {
			t.Fatalf("wealth projection: %+v", out)
		}
		app := &App{store: s, config: Config{Production: true}, templates: template.Must(template.New("dashboard.html").Parse(`{{.Production}}|{{.Overview.Mode}}|{{.Overview.NetWorthText}}|{{.Overview.AvailableText}}`))}
		req := httptest.NewRequest("GET", "/dashboard", nil)
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		app.dashboard(w, req)
		if w.Code != 200 || !strings.HasPrefix(w.Body.String(), "true|manual|") || strings.Contains(w.Body.String(), "284.350") || strings.Contains(w.Body.String(), "22.360") {
			t.Fatalf("synthetic dashboard: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestProductionPublicOriginCookiesLogoutAndReadiness(t *testing.T) {
	s, server, _, _ := productionFixture(t)
	res := call(t, server, "GET", "/login", "", nil, "")
	cookies := res.Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("unsafe login cookie: %v", cookies)
	}
	form := url.Values{"csrf": {cookies[0].Value}, "password": {"production-test-password-1234"}}
	request, _ := http.NewRequest("POST", server.URL+"/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://chris.example.test")
	request.AddCookie(cookies[0])
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	logged, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	logged.Body.Close()
	if logged.StatusCode != 303 || !logged.Cookies()[0].Secure {
		t.Fatalf("public login: %d", logged.StatusCode)
	}
	sessionCookies := logged.Cookies()
	var csrf string
	if err := s.DB.QueryRow(context.Background(), `SELECT csrf FROM sessions WHERE token_hash=$1`, digest(sessionCookies[0].Value)).Scan(&csrf); err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"http://internal:18480", "https://chris.example.test"} {
		req, _ := http.NewRequest("POST", server.URL+"/logout", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", csrf)
		req.AddCookie(sessionCookies[0])
		out, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		out.Body.Close()
		want := 403
		if origin == "https://chris.example.test" {
			want = 200
		}
		if out.StatusCode != want {
			t.Fatalf("origin %s: %d", origin, out.StatusCode)
		}
	}
	if out := call(t, server, "GET", "/api/state", "", sessionCookies, ""); out.StatusCode != 401 {
		t.Fatal("public logout left an active session")
	}
	for _, path := range []string{"/healthz", "/readyz"} {
		out := call(t, server, "GET", path, "", nil, "")
		body, _ := io.ReadAll(out.Body)
		if out.StatusCode != 200 || !strings.Contains(string(body), `"mode":"production"`) {
			t.Fatalf("%s: %d %s", path, out.StatusCode, body)
		}
	}
	if _, err := s.DB.Exec(context.Background(), `DELETE FROM schema_migrations WHERE version=2`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.DB.Exec(context.Background(), `INSERT INTO schema_migrations(version) VALUES(2) ON CONFLICT DO NOTHING`)
	})
	if out := call(t, server, "GET", "/readyz", "", nil, ""); out.StatusCode != 503 {
		t.Fatalf("readiness accepted missing schema: %d", out.StatusCode)
	}
}

func TestProductionManualFinanceSurvivesConnectionRestartAndStreams(t *testing.T) {
	s, server, cookies, csrf := productionFixture(t)
	post := func(values url.Values) *http.Response {
		t.Helper()
		values.Set("csrf", csrf)
		req, _ := http.NewRequest("POST", server.URL+"/expenses/actions", strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Origin", "https://chris.example.test")
		req.AddCookie(cookies[0])
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { res.Body.Close() })
		return res
	}
	categoryRes := post(url.Values{"action": {"category-create"}, "name": {"Educação pessoal"}, "color": {"#8bcebb"}})
	var category struct {
		Item finance.Category `json:"item"`
	}
	if err := json.NewDecoder(categoryRes.Body).Decode(&category); err != nil || categoryRes.StatusCode != 201 {
		t.Fatalf("production category: status=%d err=%v", categoryRes.StatusCode, err)
	}
	for _, values := range []url.Values{
		{"action": {"expense-create"}, "description": {"Livro de estudo"}, "date": {"2026-10-27"}, "amount": {"125,90"}, "category_id": {category.Item.ID}, "account": {"Carteira pessoal"}, "status": {"confirmed"}, "idempotency_key": {"production-expense-test"}},
		{"action": {"net-worth"}, "assets": {"5000,00"}, "liabilities": {"1250,00"}, "date": {"2026-10-27"}, "idempotency_key": {"production-net-worth-test"}},
	} {
		if res := post(values); res.StatusCode != 201 {
			body, _ := io.ReadAll(res.Body)
			t.Fatalf("production finance: %d %s", res.StatusCode, body)
		}
	}
	server.Close()
	s.Close()
	reopened, err := memory.Open(context.Background(), os.Getenv("HTTP_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.Close)
	restarted := httptest.NewServer(New(reopened, Config{Production: true, Password: "production-test-password-1234", PublicURL: "https://chris.example.test"}))
	t.Cleanup(restarted.Close)
	out := call(t, restarted, "GET", "/api/finance/summary?month=2026-10", "", cookies, "")
	var summary FinanceSummary
	if err := json.NewDecoder(out.Body).Decode(&summary); err != nil || out.StatusCode != 200 {
		t.Fatalf("restarted summary: %d %v", out.StatusCode, err)
	}
	if summary.ExpenseCents != 12590 || summary.NetWorthCents == nil || *summary.NetWorthCents != 375000 || summary.TopCategory != "Educação pessoal" || summary.PendingJobs != 0 || summary.FailedJobs != 0 {
		t.Fatalf("production data lost or synthetic jobs reported: %+v", summary)
	}
	events := call(t, restarted, "GET", "/api/finance/events?once=1&month=2026-10", "", cookies, "")
	body, _ := io.ReadAll(events.Body)
	if events.StatusCode != 200 || !strings.Contains(string(body), "event: finance.snapshot") || !strings.Contains(string(body), `"net_worth_cents":375000`) || strings.Contains(string(body), "Livro de estudo") || strings.Contains(string(body), "Carteira pessoal") {
		t.Fatalf("production SSE: %d %s", events.StatusCode, body)
	}
}

func TestProductionFlagSurvivesNativeFormErrors(t *testing.T) {
	s, _, cookies, csrf := productionFixture(t)
	tpl := template.Must(template.New("").Parse(`{{define "login.html"}}{{.Production}}{{end}}{{define "expenses.html"}}{{.Production}}|{{.FormError}}{{end}}{{define "dashboard.html"}}{{.Production}}|{{.FormError}}{{end}}`))
	app := &App{store: s, config: Config{Production: true, PublicURL: "https://chris.example.test"}, templates: tpl}
	for _, tc := range []struct {
		method, path string
		values       url.Values
		want         int
	}{
		{"GET", "/login", nil, 200},
		{"GET", "/expenses", nil, 200},
		{"POST", "/expenses/actions", url.Values{"csrf": {csrf}, "action": {"category-create"}, "name": {""}, "color": {"#8bcebb"}}, 400},
		{"POST", "/expenses/actions", url.Values{"csrf": {csrf}, "action": {"net-worth"}, "assets": {"invalid"}}, 400},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "https://chris.example.test")
		req.AddCookie(cookies[0])
		w := httptest.NewRecorder()
		switch tc.path {
		case "/login":
			app.login(w, req)
		case "/expenses":
			app.expensesPage(w, req)
		default:
			app.financeAction(w, req)
		}
		if w.Code != tc.want || !strings.HasPrefix(w.Body.String(), "true") {
			t.Fatalf("production form %s %v: %d %s", tc.path, tc.values, w.Code, w.Body.String())
		}
	}
}

func TestReadinessBoundsDatabaseWaitAndRejectsMissingCoreTable(t *testing.T) {
	s, server, _, _ := productionFixture(t)
	ctx := context.Background()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `LOCK TABLE schema_migrations IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res := call(t, server, "GET", "/readyz", "", nil, "")
	if res.StatusCode != 503 || time.Since(start) > 4*time.Second {
		t.Fatalf("readiness did not bound database wait: %d %v", res.StatusCode, time.Since(start))
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `ALTER TABLE finance_audit RENAME TO finance_audit_readiness_test`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.DB.Exec(ctx, `ALTER TABLE finance_audit_readiness_test RENAME TO finance_audit`) })
	if out := call(t, server, "GET", "/readyz", "", nil, ""); out.StatusCode != 503 {
		t.Fatalf("readiness accepted missing audit table: %d", out.StatusCode)
	}
}
