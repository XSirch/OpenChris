package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAuthCacheEncryptsAndRejectsTampering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "account.enc")
	cache := authCache{path: path, key: bytes.Repeat([]byte{7}, 32)}
	secret := []byte(`{"tokens":{"access_token":"synthetic-secret"}}`)
	if err := cache.save(secret); err != nil {
		t.Fatal(err)
	}
	ciphertext, err := os.ReadFile(path)
	if err != nil || bytes.Contains(ciphertext, []byte("synthetic-secret")) {
		t.Fatal("plaintext cache", err)
	}
	got, err := cache.load()
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatal("cache roundtrip", err)
	}
	ciphertext[len(ciphertext)-1] ^= 1
	os.WriteFile(path, ciphertext, 0600)
	if _, err := cache.load(); err == nil {
		t.Fatal("accepted modified ciphertext")
	}
}

func testRunner(t *testing.T, loggedIn bool) *Runner {
	t.Helper()
	dir := t.TempDir()
	r, err := NewRunner(RunnerConfig{Binary: os.Args[0], StateDir: dir, TempDir: dir, Key: bytes.Repeat([]byte{3}, 32), Token: strings.Repeat("t", 40), Timeout: 2 * time.Second, prefixArgs: []string{"-test.run=TestCLIHelperProcess", "--", "--codex-helper"}})
	if err != nil {
		t.Fatal(err)
	}
	if loggedIn {
		if err := r.cache.save([]byte(`{"tokens":{"access_token":"synthetic"}}`)); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestRunnerRequiresLoginAndValidCompletion(t *testing.T) {
	r := testRunner(t, false)
	if state := r.Status(context.Background()).State; state != "not_authenticated" {
		t.Fatalf("state %s", state)
	}
	t.Setenv("DATABASE_URL", "must-never-reach-codex")
	t.Setenv("PLUGGY_CLIENT_SECRET", "must-never-reach-codex")
	for _, tc := range []struct{ prompt, code string }{{"ok", ""}, {"incomplete", "incomplete"}, {"tool", "policy_violation"}, {"invalid", "invalid_output"}, {"timeout", "failed_unknown"}, {"oversized", "output_limit"}, {"quota", "rate_limited"}, {"auth", "not_authenticated"}, {"diagnostic", ""}, {"diagnostic_incomplete", "incomplete"}, {"diagnostic_then_tool", "policy_violation"}} {
		t.Run(tc.prompt, func(t *testing.T) {
			r := testRunner(t, true)
			request := Request{RunID: "00000000-0000-4000-8000-000000000002", AgentID: "A01", Context: json.RawMessage(fmt.Sprintf(`{"current":%q,"sources":[{"id":"message:1"}]}`, tc.prompt))}
			result, err := r.Run(context.Background(), request)
			if tc.code == "" {
				if err != nil || result.Reply != "Análise de teste." || result.Usage.InputTokens == nil || *result.Usage.InputTokens != 11 {
					t.Fatalf("result %+v error %v", result, err)
				}
			} else if Code(err) != tc.code {
				t.Fatalf("code=%s want=%s err=%v", Code(err), tc.code, err)
			}
		})
	}
}

func TestRunnerHTTPAuthenticatesBeforeReadingAndRejectsConcurrentWork(t *testing.T) {
	r := testRunner(t, true)
	server := httptest.NewServer(r)
	defer server.Close()
	response, err := http.Get(server.URL + "/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal(response.StatusCode)
	}
	client, err := NewClient(server.URL, strings.Repeat("t", 40))
	if err != nil {
		t.Fatal(err)
	}
	status, err := client.Status(context.Background())
	if err != nil || status.State != "available" {
		t.Fatal(status, err)
	}
	r.gate <- struct{}{}
	defer func() { <-r.gate }()
	_, err = client.Run(context.Background(), Request{RunID: "00000000-0000-4000-8000-000000000002", AgentID: "A01", Context: json.RawMessage(`{"current":"ok"}`)})
	if Code(err) != "busy" {
		t.Fatal(err)
	}
}

func TestClientNeverFollowsRedirects(t *testing.T) {
	called := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	client, _ := NewClient(redirect.URL, strings.Repeat("t", 40))
	if _, err := client.Status(context.Background()); err == nil || called {
		t.Fatal("followed redirect")
	}
}

// Child process of the real test binary; never contacts a model provider.
func TestCLIHelperProcess(t *testing.T) {
	idx := slices.Index(os.Args, "--codex-helper")
	if idx < 0 {
		return
	}
	args := os.Args[idx+1:]
	if slices.Contains(args, "status") {
		if _, err := os.Stat(filepath.Join(os.Getenv("CODEX_HOME"), "auth.json")); err != nil {
			os.Exit(1)
		}
		fmt.Println("Logged in using ChatGPT")
		os.Exit(0)
	}
	for _, name := range []string{"DATABASE_URL", "PLUGGY_CLIENT_SECRET", "CODEX_RUNNER_TOKEN", "CODEX_AUTH_KEY"} {
		if os.Getenv(name) != "" {
			os.Exit(8)
		}
	}
	for _, value := range []string{"read-only", "--ignore-user-config", "--ephemeral", "features.shell_tool=false", "features.view_image=false", "web_search=\"disabled\""} {
		if !slices.Contains(args, value) {
			os.Exit(9)
		}
	}
	var input struct {
		Context struct {
			Current string `json:"current"`
			Signal  string `json:"signal"`
		} `json:"context"`
	}
	json.NewDecoder(os.Stdin).Decode(&input)
	switch input.Context.Current {
	case "quota":
		fmt.Println(`{"type":"turn.failed","error":{"message":"usage limit reached"}}`)
		fmt.Fprintln(os.Stderr, "429 usage limit reached")
		os.Exit(1)
	case "auth":
		fmt.Println(`{"type":"error","message":"refresh token expired"}`)
		fmt.Fprintln(os.Stderr, "401 refresh token expired")
		os.Exit(1)
	case "hold":
		os.WriteFile(input.Context.Signal, []byte("ready"), 0600)
		time.Sleep(time.Second)
	case "timeout":
		time.Sleep(10 * time.Second)
	case "oversized":
		fmt.Print(strings.Repeat("x", 1100000))
		os.Exit(0)
	case "incomplete":
		fmt.Println(`{"type":"turn.started"}`)
		os.Exit(0)
	case "tool":
		fmt.Println(`{"type":"item.started","item":{"type":"command_execution"}}`)
		os.Exit(0)
	case "web_search":
		fmt.Println(`{"type":"item.completed","item":{"type":"web_search"}}`)
		os.Exit(0)
	case "diagnostic", "diagnostic_incomplete", "diagnostic_then_tool":
		fmt.Println(`{"type":"item.completed","item":{"id":"item_0","type":"error","message":"Code Mode is unavailable because code-mode host is disabled. Code mode will fail closed; enable features.code_mode_host and install codex-code-mode-host."}}`)
		if input.Context.Current == "diagnostic_incomplete" {
			fmt.Println(`{"type":"turn.started"}`)
			os.Exit(0)
		}
		if input.Context.Current == "diagnostic_then_tool" {
			fmt.Println(`{"type":"item.started","item":{"type":"command_execution"}}`)
			os.Exit(0)
		}
	}
	result := `{"reply":"Análise de teste.","episode":"Conversa de teste.","evidence":["message:1"],"assumptions":[],"unknowns":[]}`
	if input.Context.Current == "invalid" {
		result = `{"reply":"bad","extra":"forbidden"}`
	}
	if input.Context.Current == "strategy" {
		result = strings.TrimSuffix(result, "}") + `,"strategy":{"title":"Estudo de teste","hypothesis":"Pesquisar condições.","mandate":"long_term","time_horizon":"Cinco anos","conditions":[],"invalidation_conditions":[],"eligible_instruments":[],"risks":["Inflação"],"evidence":["message:1"]}}`
	}
	if i := slices.Index(args, "-o"); i >= 0 {
		os.WriteFile(args[i+1], []byte(result), 0600)
	}
	fmt.Println(`{"type":"turn.completed","usage":{"input_tokens":11,"output_tokens":7}}`)
	os.Exit(0)
}
