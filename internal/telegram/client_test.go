package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{Enabled: true, BotToken: "123456:synthetic_token_for_local_tests", WebhookSecret: strings.Repeat("s", 32), PublicURL: "https://example.test/api/telegram/webhook", AllowedUserID: 456, AllowedChatID: 456}
}

// Removing an enabled configuration requirement must allow one of these unsafe configurations.
func TestConfigRequiresPrivateCredentialsOnlyWhenEnabled(t *testing.T) {
	if err := (Config{}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := testConfig().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.BotToken = "" },
		func(c *Config) { c.BotToken = "123:bad/redirect" },
		func(c *Config) { c.WebhookSecret = "short" },
		func(c *Config) { c.WebhookSecret = strings.Repeat("!", 32) },
		func(c *Config) { c.AllowedUserID = 0 },
		func(c *Config) { c.AllowedChatID = -1 },
		func(c *Config) { c.PublicURL = "http://example.test/hook" },
		func(c *Config) { c.PublicURL = "https://user:password@example.test/hook" },
		func(c *Config) { c.PublicURL = "https://example.test/hook?token=secret" },
	} {
		cfg := testConfig()
		mutate(&cfg)
		if err := cfg.Validate(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unsafe config accepted: %v", err)
		}
	}
}

// A wrong method path or malformed request must break this real HTTP exchange.
func TestClientUsesTelegramProtocolAndProtectsMessage(t *testing.T) {
	cfg := testConfig()
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unsafe request %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		method := strings.TrimPrefix(r.URL.Path, "/bot"+cfg.BotToken+"/")
		methods = append(methods, method)
		switch method {
		case "getMe":
			fmt.Fprint(w, `{"ok":true,"result":{"id":123456,"is_bot":true,"first_name":"Synthetic"}}`)
		case "getWebhookInfo":
			fmt.Fprint(w, `{"ok":true,"result":{"url":"","pending_update_count":0}}`)
		case "setWebhook":
			var body struct {
				URL         string   `json:"url"`
				Secret      string   `json:"secret_token"`
				Updates     []string `json:"allowed_updates"`
				Connections int      `json:"max_connections"`
				Drop        bool     `json:"drop_pending_updates"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.URL != cfg.PublicURL || body.Secret != cfg.WebhookSecret || len(body.Updates) != 1 || body.Updates[0] != "message" || body.Connections != 1 || body.Drop {
				t.Errorf("unsafe webhook request: %+v", body)
			}
			fmt.Fprint(w, `{"ok":true,"result":true}`)
		case "sendMessage":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["chat_id"] != float64(456) || body["text"] != "Resumo agregado" || body["protect_content"] != true || body["parse_mode"] != nil {
				t.Errorf("unsafe message: %+v", body)
			}
			fmt.Fprint(w, `{"ok":true,"result":{"message_id":88,"date":1,"chat":{"id":456,"type":"private"},"text":"Resumo agregado"}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.Error(w, "bad", 400)
		}
	}))
	defer server.Close()
	client, err := NewClientWithBaseURL(cfg.BotToken, server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewWithClient(nil, cfg, nil, client)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	id, err := client.SendMessage(context.Background(), 456, "Resumo agregado")
	if err != nil || id != 88 {
		t.Fatalf("send: %d %v", id, err)
	}
	if strings.Join(methods, ",") != "getMe,getWebhookInfo,setWebhook,sendMessage" {
		t.Fatalf("methods: %v", methods)
	}
}

func TestSetupRefusesForeignWebhookAndMismatchedBot(t *testing.T) {
	for _, body := range []string{`{"ok":true,"result":{"url":"https://elsewhere.test/hook"}}`, `{"ok":true,"result":{"url":"https://example.test/api/telegram/webhook"}}`} {
		t.Run(body, func(t *testing.T) {
			cfg := testConfig()
			set := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "getMe"):
					fmt.Fprint(w, `{"ok":true,"result":{"id":123456,"is_bot":true}}`)
				case strings.HasSuffix(r.URL.Path, "getWebhookInfo"):
					fmt.Fprint(w, body)
				case strings.HasSuffix(r.URL.Path, "setWebhook"):
					set = true
					fmt.Fprint(w, `{"ok":true,"result":true}`)
				}
			}))
			defer server.Close()
			client, _ := NewClientWithBaseURL(cfg.BotToken, server.URL, server.Client())
			s, _ := NewWithClient(nil, cfg, nil, client)
			err := s.Setup(context.Background())
			foreign := strings.Contains(body, "elsewhere")
			if foreign && (err == nil || set) {
				t.Fatal("foreign webhook was replaced")
			}
			if !foreign && (err != nil || !set) {
				t.Fatalf("owned webhook not refreshed: %v", err)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":true,"result":{"id":999,"is_bot":true}}`)
	}))
	defer server.Close()
	client, _ := NewClientWithBaseURL(testConfig().BotToken, server.URL, server.Client())
	s, _ := NewWithClient(nil, testConfig(), nil, client)
	if err := s.Setup(context.Background()); err == nil {
		t.Fatal("wrong bot identity accepted")
	}
}

func TestClientSanitizesFailureAndRejectsRedirect(t *testing.T) {
	cfg := testConfig()
	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/"+cfg.BotToken, http.StatusFound)
	}))
	client, _ := NewClientWithBaseURL(cfg.BotToken, server.URL, server.Client())
	_, err := client.SendMessage(context.Background(), 456, "safe")
	if err == nil || redirected || strings.Contains(err.Error(), cfg.BotToken) || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("redirect or secret leak: %v", err)
	}
	server.Close()
	_, err = client.SendMessage(context.Background(), 456, "safe")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !apiErr.Ambiguous || strings.Contains(err.Error(), cfg.BotToken) {
		t.Fatalf("unsafe transport error: %v", err)
	}
}

func TestClientBoundsRateLimitAndTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		fmt.Fprint(w, `{"ok":false,"error_code":429,"description":"do not expose this token","parameters":{"retry_after":999999999}}`)
	}))
	defer server.Close()
	client, _ := NewClientWithBaseURL(testConfig().BotToken, server.URL, &http.Client{})
	_, err := client.SendMessage(context.Background(), 456, "safe")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 429 || apiErr.RetryAfter != time.Hour || apiErr.Ambiguous || strings.Contains(err.Error(), "expose") {
		t.Fatalf("rate limit: %#v %v", apiErr, err)
	}
	if client.http.Timeout <= 0 || client.http.Timeout > 15*time.Second {
		t.Fatalf("unbounded timeout %s", client.http.Timeout)
	}
}

func TestClientRejectsUnsafeOutgoingTextBeforeHTTP(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":89}}`)
	}))
	defer server.Close()
	client, _ := NewClientWithBaseURL(testConfig().BotToken, server.URL, server.Client())
	for _, text := range []string{"", " ", "\x00", string([]byte{0xff}), strings.Repeat("á", 4097)} {
		if _, err := client.SendMessage(context.Background(), 456, text); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unsafe outgoing accepted: %v", err)
		}
	}
	if _, err := client.SendMessage(context.Background(), 0, "safe"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid destination accepted: %v", err)
	}
	if requests != 0 {
		t.Fatalf("unsafe HTTP requests: %d", requests)
	}
}
