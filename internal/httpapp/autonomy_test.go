package httpapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openchris/conselho/internal/autonomy"
	"openchris/conselho/internal/codex"
	"openchris/conselho/internal/council"
	"openchris/conselho/internal/memory"
)

func TestAutonomyRoutesRequireSessionCSRFAndExplicitPause(t *testing.T) {
	store, _, cookies, csrf := productionFixture(t)
	if _, err := store.DB.Exec(context.Background(), `TRUNCATE autonomy_state,autonomy_runs CASCADE`); err != nil {
		t.Fatal(err)
	}
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(codex.Status{State: "available", Model: codex.DefaultModel})
	}))
	defer runner.Close()
	cfg := Config{Production: true, Password: "production-test-password-1234", PublicURL: "https://chris.example.test", AutonomyEnabled: true, AutonomyDailyLimit: 20, Codex: council.Config{Enabled: true, RunnerURL: runner.URL, RunnerToken: strings.Repeat("t", 40), DailyLimit: 30}}
	app := New(store, cfg)
	server := httptest.NewServer(app)
	defer server.Close()
	for _, path := range []string{"/api/autonomy", "/api/autonomy/strategies", "/api/autonomy/runs/00000000-0000-4000-8000-000000000099"} {
		response := call(t, server, "GET", path, "", nil, "")
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatalf("anonymous %s: %d", path, response.StatusCode)
		}
	}
	for _, check := range []struct {
		body, token string
		want        int
	}{{`{"paused":true}`, "", 403}, {`{}`, csrf, 400}, {`{"paused":true,"extra":1}`, csrf, 400}, {`{"paused":true} {}`, csrf, 400}, {`{"paused":true}`, csrf, 200}} {
		response := call(t, server, "POST", "/api/autonomy/pause", check.body, cookies, check.token)
		response.Body.Close()
		if response.StatusCode != check.want {
			t.Fatalf("pause=%s status%d want%d", check.body, response.StatusCode, check.want)
		}
	}
	response := call(t, server, "GET", "/api/autonomy", "", cookies, "")
	defer response.Body.Close()
	var status autonomy.Status
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil || response.StatusCode != 200 || !status.Enabled || !status.Paused {
		t.Fatalf("status %+v %v", status, err)
	}
	response = call(t, server, "GET", "/api/autonomy/runs/invalid", "", cookies, "")
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatal(response.StatusCode)
	}
	response = call(t, server, "POST", "/api/autonomy/run", `{}`, cookies, csrf)
	response.Body.Close()
	if response.StatusCode != 409 {
		t.Fatalf("paused accepted run:%d", response.StatusCode)
	}
	var count int
	if err := store.DB.QueryRow(context.Background(), `SELECT count(*) FROM autonomy_runs WHERE user_id=$1`, memory.UserID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("pause generated work:%d", count)
	}
}
