package httpapp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"openchris/conselho/internal/memory"
)

func fixture(t *testing.T) (*memory.Store, *httptest.Server) {
	t.Helper()
	dsn := os.Getenv("HTTP_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("HTTP_TEST_DATABASE_URL must name the isolated HTTP test database")
	}
	s, err := memory.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	if s.DB.Config().ConnConfig.Database != "conselho_http_test" {
		t.Fatal("refusing cleanup outside conselho_http_test")
	}
	_, err = s.DB.Exec(context.Background(), `TRUNCATE conversation_events,jobs,episodes,memory_facts,context_manifests,stream_events,sessions RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(s, Config{Demo: true, Password: "demo-conselho", TelegramSecret: "synthetic-webhook", BenchToken: "stack-benchmark-local", SessionTTL: time.Hour}))
	t.Cleanup(server.Close)
	t.Cleanup(s.Close)
	return s, server
}
func call(t *testing.T, server *httptest.Server, method, path, body string, cookies []*http.Cookie, csrf string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}
func login(t *testing.T, server *httptest.Server) ([]*http.Cookie, string) {
	t.Helper()
	res := call(t, server, "GET", "/login", "", nil, "")
	body, _ := io.ReadAll(res.Body)
	match := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(string(body))
	if len(match) != 2 {
		t.Fatalf("login CSRF missing: %s", body)
	}
	form := url.Values{"password": {"demo-conselho"}, "csrf": {match[1]}}
	req, _ := http.NewRequest("POST", server.URL+"/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range res.Cookies() {
		req.AddCookie(c)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	out, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Body.Close()
	if out.StatusCode != 303 {
		b, _ := io.ReadAll(out.Body)
		t.Fatalf("login %d %s", out.StatusCode, b)
	}
	cookies := out.Cookies()
	state := call(t, server, "GET", "/api/state", "", cookies, "")
	var data map[string]any
	if err = json.NewDecoder(state.Body).Decode(&data); err != nil {
		t.Fatal(err)
	}
	csrf, _ := data["csrf"].(string)
	if csrf == "" {
		t.Fatal("session csrf missing")
	}
	return cookies, csrf
}

func TestAuthenticationCSRFLogoutAndExpiry(t *testing.T) {
	s, server := fixture(t)
	if res := call(t, server, "GET", "/api/state", "", nil, ""); res.StatusCode != 401 {
		t.Fatalf("unauthenticated state %d", res.StatusCode)
	}
	if res := call(t, server, "GET", "/api/office/events", "", nil, ""); res.StatusCode != 401 {
		t.Fatalf("unauthenticated stream %d", res.StatusCode)
	}
	cookies, csrf := login(t, server)
	input := `{"text":"Olá","idempotency_key":"one"}`
	if res := call(t, server, "POST", "/api/conversations/turn", input, cookies, ""); res.StatusCode != 403 {
		t.Fatalf("missing csrf %d", res.StatusCode)
	}
	if res := call(t, server, "POST", "/api/conversations/turn", input, cookies, csrf); res.StatusCode != 202 {
		t.Fatalf("valid turn %d", res.StatusCode)
	}
	if res := call(t, server, "POST", "/logout", "", cookies, csrf); res.StatusCode != 200 {
		t.Fatalf("logout %d", res.StatusCode)
	}
	if res := call(t, server, "GET", "/api/state", "", cookies, ""); res.StatusCode != 401 {
		t.Fatal("session survived logout")
	}
	cookies, _ = login(t, server)
	if _, err := s.DB.Exec(context.Background(), `UPDATE sessions SET expires_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if res := call(t, server, "GET", "/api/state", "", cookies, ""); res.StatusCode != 401 {
		t.Fatal("expired session accepted")
	}
}

func TestTelegramIdentitySecretAndPrivateChat(t *testing.T) {
	s, server := fixture(t)
	for _, tc := range []struct {
		secret, body string
		want         int
	}{
		{"wrong", `{"update_id":1,"message":{"text":"Olá","from":{"id":1001},"chat":{"id":1001,"type":"private"}}}`, 403},
		{"synthetic-webhook", `{"update_id":2,"message":{"text":"Olá","from":{"id":9999},"chat":{"id":1001,"type":"private"}}}`, 403},
		{"synthetic-webhook", `{"update_id":3,"message":{"text":"Olá","from":{"id":1001},"chat":{"id":1001,"type":"group"}}}`, 403},
		{"synthetic-webhook", `{"update_id":4,"message":{"text":"Olá","from":{"id":1001},"chat":{"id":1001,"type":"private"},"forward_origin":{"type":"user"}}}`, 403},
		{"synthetic-webhook", `{"update_id":5,"message":{"text":"Minha reserva é de 6 meses","from":{"id":1001},"chat":{"id":1001,"type":"private"}}}`, 200},
	} {
		req, _ := http.NewRequest("POST", server.URL+"/webhooks/telegram", strings.NewReader(tc.body))
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", tc.secret)
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != tc.want {
			t.Fatalf("want %d got %d", tc.want, res.StatusCode)
		}
	}
	snap, _ := s.Snapshot(context.Background(), "")
	if len(snap.Messages) != 1 {
		t.Fatalf("unauthorized messages persisted: %d", len(snap.Messages))
	}
	if snap.Pending != 1 {
		t.Fatal("webhook processed inline before ACK")
	}
}

func TestSimulatorAndDashboardSharePersistedMemory(t *testing.T) {
	s, server := fixture(t)
	cookies, csrf := login(t, server)
	res := call(t, server, "POST", "/demo/telegram", `{"text":"Minha reserva é de 12 meses","idempotency_key":"42"}`, cookies, csrf)
	if res.StatusCode != 202 {
		t.Fatalf("simulator: %d", res.StatusCode)
	}
	if _, err := s.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	res = call(t, server, "POST", "/api/conversations/turn", `{"text":"Qual é minha reserva?","idempotency_key":"43"}`, cookies, csrf)
	if res.StatusCode != 202 {
		t.Fatal(res.StatusCode)
	}
	if _, err := s.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	snap, _ := s.Snapshot(context.Background(), "")
	if len(snap.Facts) != 1 || snap.Facts[0].Value != "12" || snap.Messages[0].Channel != "dashboard" || !strings.Contains(snap.Messages[0].Text, "12 meses") {
		t.Fatalf("cross-channel HTTP: %+v", snap)
	}
	// Invalid bytes/extra JSON and cross-origin mutation must not enter the journal.
	for _, body := range []string{`{"text":"Olá","idempotency_key":"x"} {}`, string([]byte{0xff}), `{"text":"","idempotency_key":"empty"}`} {
		if out := call(t, server, "POST", "/api/conversations/turn", body, cookies, csrf); out.StatusCode != 400 {
			t.Fatalf("invalid body accepted: %d", out.StatusCode)
		}
	}
	req, _ := http.NewRequest("POST", server.URL+"/api/conversations/turn", strings.NewReader(`{"text":"Olá","idempotency_key":"evil"}`))
	req.Header.Set("Origin", "https://evil.invalid")
	req.Header.Set("X-CSRF-Token", csrf)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	out, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out.Body.Close()
	if out.StatusCode != 403 {
		t.Fatal("cross origin accepted")
	}
}

func TestSSEUsesPersistedCursorAndSanitizedProjection(t *testing.T) {
	s, server := fixture(t)
	cookies, _ := login(t, server)
	if _, err := s.Ingest(context.Background(), memory.Input{Channel: "telegram", Key: "sensitive", Text: "CPF 12345678901"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/office/events?once=1", nil)
	req.Header.Set("Last-Event-ID", "1")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || strings.Contains(string(body), "12345678901") || strings.Contains(string(body), "id: 1\n") || !strings.Contains(string(body), "agent.completed") {
		t.Fatalf("SSE projection: %d %s", res.StatusCode, body)
	}
}

func TestPublicDeploymentRejectsDemoCredentialsAndHTTP(t *testing.T) {
	for _, cfg := range []Config{
		{Demo: true, Password: "demo-conselho", TelegramSecret: "synthetic-webhook", PublicURL: "https://conselho.example.com"},
		{Demo: true, Password: strings.Repeat("x", 24), TelegramSecret: strings.Repeat("s", 32), PublicURL: "http://conselho.example.com"},
	} {
		if err := cfg.Validate(); err == nil {
			t.Fatal("unsafe public configuration accepted")
		}
	}
	cfg := Config{Demo: true, Password: strings.Repeat("x", 24), TelegramSecret: strings.Repeat("s", 32), PublicURL: "https://conselho.example.com"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	app := &App{config: cfg}
	cookie := app.cookie("conselho_session", "opaque", 3600)
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("public session cookie is not protected")
	}
}

func TestPublicDeploymentRejectsUnreplacedPlaceholders(t *testing.T) {
	cfg := Config{Demo: true, Password: "SUBSTITUA_POR_UMA_SENHA_ALEATORIA_DE_32_CARACTERES", TelegramSecret: "SUBSTITUA_POR_UM_SEGREDO_ALEATORIO_DE_64_CARACTERES", PublicURL: "https://conselho.example.com"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("unreplaced public example credentials accepted")
	}
}

func TestBenchmarkRejectsInvalidUnicode(t *testing.T) {
	_, server := fixture(t)
	req, _ := http.NewRequest("POST", server.URL+"/bench/webhook", strings.NewReader(`{"event_id":"invalid-unicode","text":"\ud800"}`))
	req.Header.Set("Authorization", "Bearer stack-benchmark-local")
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("invalid Unicode accepted by benchmark: %d", res.StatusCode)
	}
}

func TestMemoryHistoryRequiresSessionAndPreservesProvenance(t *testing.T) {
	s, server := fixture(t)
	if res := call(t, server, "GET", "/api/memory/history", "", nil, ""); res.StatusCode != 401 {
		t.Fatalf("history without session: %d", res.StatusCode)
	}
	cookies, _ := login(t, server)
	ctx := context.Background()
	for _, in := range []memory.Input{{Channel: "telegram", Key: "design-old", Text: "Minha reserva é de 6 meses"}, {Channel: "dashboard", Key: "design-new", Text: "Quero mudar minha reserva para 12 meses"}} {
		if _, err := s.Ingest(ctx, in); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
	}
	res := call(t, server, "GET", "/api/memory/history", "", cookies, "")
	var result struct {
		Versions []memory.FactVersion `json:"versions"`
	}
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || len(result.Versions) != 2 {
		t.Fatalf("missing versions: %d %+v", res.StatusCode, result)
	}
	current, old := result.Versions[0], result.Versions[1]
	if current.Value != "12" || current.Status != "active" || current.Channel != "dashboard" || current.SourceText != "Quero mudar minha reserva para 12 meses" || current.Authority != "USER_EXPLICIT" {
		t.Fatalf("current provenance: %+v", current)
	}
	if old.Value != "6" || old.Status != "superseded" || old.ValidTo == nil || old.SourceText != "Minha reserva é de 6 meses" {
		t.Fatalf("historical provenance: %+v", old)
	}
}
