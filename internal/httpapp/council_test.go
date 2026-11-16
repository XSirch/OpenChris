package httpapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"openchris/conselho/internal/codex"
	"openchris/conselho/internal/council"
	"openchris/conselho/internal/memory"
)

func TestProductionCodexConversationRequiresSessionPersistsAndExposesSafeSources(t *testing.T) {
	store, _, cookies, csrf := productionFixture(t)
	var state atomic.Value
	state.Store("available")
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("t", 40) {
			t.Error("runner service authorization missing")
		}
		if r.URL.Path == "/v1/status" {
			json.NewEncoder(w).Encode(codex.Status{State: state.Load().(string), Model: codex.DefaultModel})
			return
		}
		var in codex.Request
		json.NewDecoder(r.Body).Decode(&in)
		var data struct {
			Sources []struct {
				ID string `json:"id"`
			} `json:"sources"`
		}
		json.Unmarshal(in.Context, &data)
		json.NewEncoder(w).Encode(codex.Result{Reply: "Análise disponível para revisão.", Episode: "Conversa analisada.", Evidence: []string{data.Sources[0].ID}, Assumptions: []string{}, Unknowns: []string{"Não há fonte de mercado atual."}, Model: codex.DefaultModel})
	}))
	defer runner.Close()
	cfg := Config{Production: true, Password: "production-test-password-1234", PublicURL: "https://chris.example.test", Codex: council.Config{Enabled: true, RunnerURL: runner.URL, RunnerToken: strings.Repeat("t", 40), DailyLimit: 30}}
	application := New(store, cfg)
	server := httptest.NewServer(application)
	defer server.Close()
	for _, auth := range []struct {
		cookies []*http.Cookie
		csrf    string
		want    int
	}{{nil, "", 401}, {cookies, "", 403}} {
		response := call(t, server, "POST", "/api/conversations/turn", `{"text":"Olá","idempotency_key":"codex-http-1"}`, auth.cookies, auth.csrf)
		if response.StatusCode != auth.want {
			t.Fatal(response.StatusCode)
		}
	}
	response := call(t, server, "POST", "/api/conversations/turn", `{"text":"Cora, como organizar meus gastos?","idempotency_key":"codex-http-1"}`, cookies, csrf)
	var receipt memory.Receipt
	json.NewDecoder(response.Body).Decode(&receipt)
	if response.StatusCode != 202 || receipt.ID == 0 {
		t.Fatal(response.StatusCode, receipt)
	}
	if ok, err := application.councilService.ProcessOne(context.Background()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	response = call(t, server, "GET", fmt.Sprintf("/api/analyses/%d", receipt.ID), "", cookies, "")
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 || !strings.Contains(string(raw), "message:") || strings.Contains(string(raw), strings.Repeat("t", 40)) || strings.Contains(string(raw), runner.URL) {
		t.Fatal("unsafe or missing analysis", response.StatusCode)
	}
	response = call(t, server, "GET", fmt.Sprintf("/api/analyses/%d", receipt.ID), "", nil, "")
	if response.StatusCode != 401 {
		t.Fatal("anonymous analysis", response.StatusCode)
	}
	state.Store("not_authenticated")
	response = call(t, server, "GET", "/api/state", "", cookies, "")
	var status struct {
		Capabilities map[string]string `json:"capabilities"`
	}
	json.NewDecoder(response.Body).Decode(&status)
	if status.Capabilities["models"] != "not_authenticated" || status.Capabilities["conversation"] == "configured" {
		t.Fatal(status)
	}
	response = call(t, server, "POST", "/api/conversations/turn", `{"text":"outra pergunta","idempotency_key":"codex-http-2"}`, cookies, csrf)
	if response.StatusCode != 503 {
		t.Fatal(response.StatusCode)
	}
}
