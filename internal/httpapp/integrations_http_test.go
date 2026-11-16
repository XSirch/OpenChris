package httpapp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"openchris/conselho/internal/memory"
	"openchris/conselho/internal/telegram"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func integrationFixture(t *testing.T) (*memory.Store, *httptest.Server, []*http.Cookie, string) {
	t.Helper()
	dsn := os.Getenv("INTEGRATIONS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("INTEGRATIONS_TEST_DATABASE_URL not set")
	}
	if !strings.Contains(dsn, "/conselho_integrations_test?") {
		t.Fatal("refusing non-test integration database")
	}
	s, err := memory.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if _, err = s.DB.Exec(context.Background(), "TRUNCATE sessions"); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Production: true, Password: "integration-http-test-password", PublicURL: "https://chris.example.test"}
	server := httptest.NewServer(New(s, cfg))
	t.Cleanup(server.Close)
	token, csrf := randomToken(), randomToken()
	if _, err = s.DB.Exec(context.Background(), "INSERT INTO sessions(token_hash,csrf,expires_at) VALUES($1,$2,now()+interval '1 hour')", digest(token), csrf); err != nil {
		t.Fatal(err)
	}
	return s, server, []*http.Cookie{{Name: "conselho_session", Value: token}}, csrf
}
func TestIntegrationStatusRequiresSessionAndDoesNotLeakConfiguration(t *testing.T) {
	_, server, cookies, _ := integrationFixture(t)
	if res := call(t, server, "GET", "/api/integrations", "", nil, ""); res.StatusCode != 401 {
		t.Fatalf("anonymous status=%d", res.StatusCode)
	}
	res := call(t, server, "GET", "/api/integrations", "", cookies, "")
	var status map[string]any
	if err := json.NewDecoder(res.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status=%d", res.StatusCode)
	}
	for _, key := range []string{"telegram", "pluggy"} {
		provider, ok := status[key].(map[string]any)
		if !ok || provider["enabled"] != false || provider["configured"] != false {
			t.Fatalf("misleading integration %s: %v", key, provider)
		}
		for _, sensitive := range []string{"token", "apiKey", "clientSecret", "webhook_secret", "bot_token"} {
			if _, found := provider[sensitive]; found {
				t.Fatalf("secret key leaked: %s", sensitive)
			}
		}
	}
}
func TestIntegrationActionsRequireCSRFAndRemainInactiveWithoutCredentials(t *testing.T) {
	_, server, cookies, csrf := integrationFixture(t)
	for _, route := range []string{"/api/integrations/telegram/setup", "/api/integrations/pluggy/setup", "/api/integrations/pluggy/connect-token", "/api/integrations/pluggy/items", "/api/integrations/pluggy/sync"} {
		for _, auth := range []struct {
			cookies []*http.Cookie
			csrf    string
			want    int
		}{{nil, "", 401}, {cookies, "", 403}, {cookies, csrf, 503}} {
			res := call(t, server, "POST", route, "{}", auth.cookies, auth.csrf)
			if res.StatusCode != auth.want {
				t.Fatalf("%s status=%d want=%d", route, res.StatusCode, auth.want)
			}
		}
	}
	for _, route := range []string{"/webhooks/telegram", "/webhooks/pluggy"} {
		if res := call(t, server, "POST", route, "{}", nil, ""); res.StatusCode != http.StatusNotFound {
			t.Fatalf("disabled webhook %s=%d", route, res.StatusCode)
		}
	}
}
func TestIntegrationDataRequiresSessionAndDisabledDoesNotInventRows(t *testing.T) {
	_, server, cookies, _ := integrationFixture(t)
	if res := call(t, server, "GET", "/api/integrations/pluggy/data", "", nil, ""); res.StatusCode != 401 {
		t.Fatal(res.StatusCode)
	}
	res := call(t, server, "GET", "/api/integrations/pluggy/data", "", cookies, "")
	if res.StatusCode != 503 {
		t.Fatalf("disabled data status=%d", res.StatusCode)
	}
}
func TestIntegrationTelegramRepliesUseOnlyAggregateData(t *testing.T) {
	s, _, _, _ := integrationFixture(t)
	a := New(s, Config{Production: true, Password: "integration-http-test-password", PublicURL: "https://chris.example.test"})
	text, err := a.telegramReply(context.Background(), "/patrimonio")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Não informado") || !strings.Contains(text, "https://chris.example.test/dashboard") {
		t.Fatalf("missing honest aggregate: %q", text)
	}
	text, err = a.telegramReply(context.Background(), "quero o extrato e contas")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "quero o extrato") || !strings.Contains(text, "registrada") {
		t.Fatalf("raw input echoed or false AI response: %q", text)
	}
}

func TestConfiguredTelegramIngressAuthenticatesOwnerAndCommitsBeforeAcknowledgment(t *testing.T) {
	s, _, _, _ := integrationFixture(t)
	var remoteCalls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { remoteCalls.Add(1); w.WriteHeader(500) }))
	defer remote.Close()
	token := "123456789:synthetic_test_token"
	client, err := telegram.NewClientWithBaseURL(token, remote.URL, remote.Client())
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Production: true, Password: "integration-http-test-password", PublicURL: "https://chris.example.test", Telegram: telegram.Config{Enabled: true, BotToken: token, WebhookSecret: strings.Repeat("s", 40), PublicURL: "https://chris.example.test/webhooks/telegram", AllowedUserID: 456, AllowedChatID: 456}, TelegramClient: client}
	server := httptest.NewServer(New(s, cfg))
	defer server.Close()
	var before int
	if err = s.DB.QueryRow(context.Background(), "SELECT count(*) FROM conversation_events").Scan(&before); err != nil {
		t.Fatal(err)
	}
	update := map[string]any{"update_id": time.Now().UnixNano(), "message": map[string]any{"message_id": 88, "date": 1700000000, "from": map[string]any{"id": 456, "is_bot": false}, "chat": map[string]any{"id": 456, "type": "private"}, "text": "/patrimonio"}}
	raw, _ := json.Marshal(update)
	send := func(header string, body []byte) int {
		req, _ := http.NewRequest("POST", server.URL+"/webhooks/telegram", bytes.NewReader(body))
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", header)
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		return res.StatusCode
	}
	if code := send("", raw); code != 403 {
		t.Fatalf("unsigned ingress=%d", code)
	}
	if code := send(cfg.Telegram.WebhookSecret, raw); code != 200 {
		t.Fatalf("signed ingress=%d", code)
	}
	if code := send(cfg.Telegram.WebhookSecret, raw); code != 200 {
		t.Fatalf("duplicate ingress=%d", code)
	}
	var after, jobs int
	if err = s.DB.QueryRow(context.Background(), "SELECT count(*) FROM conversation_events").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow(context.Background(), "SELECT count(*) FROM jobs").Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if after != before+1 || jobs != 0 || remoteCalls.Load() != 0 {
		t.Fatalf("ingress not durable/idempotent/local: before=%d after=%d jobs=%d remote=%d", before, after, jobs, remoteCalls.Load())
	}
	update["message"].(map[string]any)["text"] = "other text"
	changed, _ := json.Marshal(update)
	if code := send(cfg.Telegram.WebhookSecret, changed); code != 409 {
		t.Fatalf("duplicate changed ingress=%d", code)
	}
	update["update_id"] = time.Now().UnixNano()
	update["message"].(map[string]any)["from"] = map[string]any{"id": 999, "is_bot": false}
	changed, _ = json.Marshal(update)
	if code := send(cfg.Telegram.WebhookSecret, changed); code != 403 {
		t.Fatalf("foreign owner ingress=%d", code)
	}
}
