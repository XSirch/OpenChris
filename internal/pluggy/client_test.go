package pluggy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testItem = "a5c763cb-0952-457b-9936-630f79c5b016"
const testAccount = "8a6e2c17-2817-40bb-b03d-546febc6a60a"

// MeuPluggy submits empty OAuth credentials; duplicate detection would reject
// a new authorization when an earlier consent already exists in the application.
func TestMeuPluggyConnectTokenAllowsOAuthAndScopesOwner(t *testing.T) {
	var auth, tokens atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("missing JSON content type")
		}
		switch r.URL.Path {
		case "/auth":
			auth.Add(1)
			var body map[string]string
			if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&body) != nil || body["clientId"] != "id" || body["clientSecret"] != "server-secret" {
				t.Error("bad server authentication")
			}
			fmt.Fprint(w, `{"apiKey":"server-key"}`)
		case "/connect_token":
			tokens.Add(1)
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Fatal("bad token body")
			}
			options, _ := body["options"].(map[string]any)
			if r.Method != "POST" || r.Header.Get("X-API-KEY") != "server-key" || len(body) != 1 || options["clientUserId"] != "owner" || options["avoidDuplicates"] != false {
				t.Errorf("unsafe token request: %#v", body)
			}
			if fmt.Sprint(options["products"]) != "[ACCOUNTS CREDIT_CARDS TRANSACTIONS INVESTMENTS]" {
				t.Errorf("personal connection omitted investment collection: %v", options["products"])
			}
			fmt.Fprint(w, `{"accessToken":"browser-token"}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := NewClient("id", "server-secret", WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	now := time.Now()
	c.now = func() time.Time { return now }
	for range 2 {
		if token, err := c.ConnectToken(context.Background(), "owner"); err != nil || token != "browser-token" {
			t.Fatalf("token=%q err=%v", token, err)
		}
	}
	if auth.Load() != 1 || tokens.Load() != 2 {
		t.Fatal("server key was not cached, or client token was reused")
	}
	now = now.Add(2 * time.Hour)
	if _, err := c.ConnectToken(context.Background(), "owner"); err != nil || auth.Load() != 2 {
		t.Fatalf("expired API key was reused: %v", err)
	}
}

// Missing a page or decoding money through float64 loses source evidence.
func TestPaginationPreservesExactNumbersCurrencyAndNull(t *testing.T) {
	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth" {
			fmt.Fprint(w, `{"apiKey":"key"}`)
			return
		}
		if r.URL.Path != "/accounts" || r.URL.Query().Get("itemId") != testItem || r.URL.Query().Get("pageSize") != "500" {
			t.Errorf("unexpected resource %s", r.URL)
		}
		pages = append(pages, r.URL.Query().Get("page"))
		if r.URL.Query().Get("page") == "1" {
			fmt.Fprint(w, `{"page":1,"totalPages":2,"total":2,"results":[{"id":"`+testAccount+`","balance":9007199254740993.123456789,"currencyCode":"USD","updatedAt":null}]}`)
		} else {
			fmt.Fprint(w, `{"page":2,"totalPages":2,"total":2,"results":[{"id":"other","balance":null,"currencyCode":null}]}`)
		}
	}))
	defer server.Close()
	c := NewClient("id", "secret", WithBaseURL(server.URL))
	rows, err := c.Accounts(context.Background(), testItem)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	if fmt.Sprint(pages) != "[1 2]" || rows[0]["balance"] != json.Number("9007199254740993.123456789") || rows[0]["currencyCode"] != "USD" || rows[0]["updatedAt"] != nil || rows[1]["balance"] != nil {
		t.Fatalf("source changed: %#v %v", rows, pages)
	}
}

// An untrusted pagination URL must never receive the API key.
func TestClientRejectsRedirectExternalPaginationOversizeAndInvalidIDs(t *testing.T) {
	var outside atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { outside.Add(1); fmt.Fprint(w, `{}`) }))
	defer other.Close()
	for _, mode := range []string{"redirect", "external", "large", "too-many", "bad-json"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/auth" {
					fmt.Fprint(w, `{"apiKey":"key"}`)
					return
				}
				switch mode {
				case "redirect":
					http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
				case "external":
					fmt.Fprintf(w, `{"results":[],"totalPages":1,"next":"%s/accounts"}`, other.URL)
				case "large":
					fmt.Fprint(w, strings.Repeat(" ", maxResponseBytes+1))
				case "too-many":
					fmt.Fprint(w, `{"results":[],"totalPages":100001}`)
				case "bad-json":
					fmt.Fprint(w, `{"results":[]} {"results":[]}`)
				}
			}))
			defer server.Close()
			c := NewClient("id", "secret", WithBaseURL(server.URL))
			if _, err := c.Accounts(context.Background(), testItem); err == nil {
				t.Fatal("unsafe response accepted")
			}
		})
	}
	if outside.Load() != 0 {
		t.Fatal("credentials could reach external server")
	}
	c := NewClient("id", "secret", WithBaseURL(other.URL))
	if _, err := c.Item(context.Background(), "../../payments"); err == nil {
		t.Fatal("invalid ID accepted")
	}
	if outside.Load() != 0 {
		t.Fatal("invalid ID made a request")
	}
}

func TestClientErrorsDoNotContainProviderBodyOrCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		fmt.Fprint(w, `{"message":"server-secret and user financial details"}`)
	}))
	defer server.Close()
	c := NewClient("id", "server-secret", WithBaseURL(server.URL))
	_, err := c.Item(context.Background(), testItem)
	if err == nil || strings.Contains(err.Error(), "server-secret") || strings.Contains(err.Error(), "financial details") {
		t.Fatalf("unsafe error: %v", err)
	}
}

// Changing the page count during traversal must not yield a partial snapshot.
func TestClientRejectsInconsistentOrMissingPaginationMetadata(t *testing.T) {
	for _, mode := range []string{"changed", "missing"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/auth" {
					fmt.Fprint(w, `{"apiKey":"key"}`)
					return
				}
				if mode == "missing" {
					fmt.Fprint(w, `{"results":[{"id":"one"}]}`)
					return
				}
				if r.URL.Query().Get("page") == "1" {
					fmt.Fprint(w, `{"page":1,"totalPages":2,"total":2,"results":[{"id":"one"}]}`)
				} else {
					fmt.Fprint(w, `{"page":2,"totalPages":1,"total":1,"results":[{"id":"two"}]}`)
				}
			}))
			defer server.Close()
			c := NewClient("id", "secret", WithBaseURL(server.URL))
			if _, err := c.Accounts(context.Background(), testItem); err == nil {
				t.Fatal("incomplete traversal accepted")
			}
		})
	}
}
