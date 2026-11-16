package httpapp

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"openchris/conselho/internal/autonomy"
	"openchris/conselho/internal/memory"
	"regexp"
	"strings"
	"testing"
)

func TestReportReturnTarget(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	for _, raw := range []string{"https://evil.test", "//evil.test", "/reports?next=https://evil.test", "/reports%5cevil", "/reports?run=%252f%252fevil", "/login", "/reports\r\nX: x"} {
		if got := safeReturnTarget(raw); got != "/dashboard" {
			t.Errorf("unsafe %q accepted as %q", raw, got)
		}
	}
	for _, raw := range []string{"/reports", "/reports?run=" + id, "/reports?run=" + id + "#task-3", "/dashboard?replay=" + id} {
		if got := safeReturnTarget(raw); got != raw {
			t.Errorf("lost %q: %q", raw, got)
		}
	}
}
func TestReportTemplateEscapesAndShowsFiveTasks(t *testing.T) {
	a := New(nil, Config{Demo: true, Password: "demo-conselho", TelegramSecret: "synthetic"})
	detail := autonomy.RunDetail{Run: autonomy.RunSummary{ID: "11111111-1111-4111-8111-111111111111", Status: "partial"}, Tasks: []autonomy.TaskSummary{{Stage: 1, AgentID: "A11", Status: "complete", Result: json.RawMessage(`{"reply":"<script>alert(1)</script>","unknowns":["Perfil ausente"],"assumptions":["Prazo informado"],"evidence":["ref-1"]}`)}}}
	detail.Strategies = []json.RawMessage{json.RawMessage(`{"title":"Prazo de teste","hypothesis":"Hipótese persistida","time_horizon":"Cinco anos","conditions":["Reserva declarada"],"risks":["Prazo incerto"],"version":1}`)}
	detail.Evidence = []autonomy.Evidence{{Title: "Fonte segura", URL: "https://example.com/a"}, {Title: "Fonte insegura", URL: "https://user:pass@example.com/b"}, {Title: "Protocolo inseguro", URL: "javascript:alert(1)"}}
	w := httptest.NewRecorder()
	a.render(w, "reports.html", reportPageData(autonomy.ArchivePage{}, &detail, ""))
	body := w.Body.String()
	for _, want := range []string{"task-0", "task-1", "task-2", "task-3", "task-4", "Bento", "Perfil ausente", "Prazo informado", "ref-1", "&lt;script&gt;", "Ver no escritório", "Prazo de teste", "Hipótese persistida", "Reserva declarada", "https://example.com/a"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %s", want, body)
		}
	}
	if strings.Contains(body, "https://user:pass") || strings.Contains(body, "javascript:") {
		t.Fatal("unsafe source link")
	}
	if strings.Contains(body, "<script>alert") {
		t.Fatal("unescaped report")
	}
}
func TestReportsRequiresSessionAndKeepsRun(t *testing.T) {
	a := New(nil, Config{Demo: true, Password: "demo-conselho", TelegramSecret: "synthetic"})
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/reports?run=11111111-1111-4111-8111-111111111111", nil))
	if w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "return_to=") {
		t.Fatalf("redirect %d %s", w.Code, w.Header().Get("Location"))
	}
}
func TestReportLoginRoundTripAndLegacy(t *testing.T) {
	s, server := fixture(t)
	ctx := context.Background()
	id := memory.ID()
	_, err := s.DB.Exec(ctx, `INSERT INTO autonomy_runs(id,user_id,status,reason,topic,fingerprint,created_at) VALUES($1,$2,'partial','fixture','Tema da rodada','fixture',now())`, id, memory.UserID)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, path := range []string{"/reports?run=" + id, "/dashboard?run=" + id} {
		res, err := client.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 303 {
			t.Fatalf("unauth %d", res.StatusCode)
		}
		get, err := client.Get(server.URL + res.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(get.Body)
		get.Body.Close()
		csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(string(body))
		target := regexp.MustCompile(`name="return_to" value="([^"]+)"`).FindStringSubmatch(string(body))
		if len(csrf) != 2 || len(target) != 2 {
			t.Fatalf("missing login state: %s", body)
		}
		form := url.Values{"password": {"demo-conselho"}, "csrf": {csrf[1]}, "return_to": {html.UnescapeString(target[1])}}
		req, _ := http.NewRequest("POST", server.URL+"/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, c := range get.Cookies() {
			req.AddCookie(c)
		}
		logged, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		logged.Body.Close()
		if logged.StatusCode != 303 || logged.Header.Get("Location") != "/reports?run="+id {
			t.Fatalf("lost report: %d %s", logged.StatusCode, logged.Header.Get("Location"))
		}
		req, _ = http.NewRequest("GET", server.URL+logged.Header.Get("Location"), nil)
		for _, c := range logged.Cookies() {
			req.AddCookie(c)
		}
		report, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(report.Body)
		report.Body.Close()
		if report.StatusCode != 200 || !strings.Contains(string(b), "Tema da rodada") {
			t.Fatalf("report %d %s", report.StatusCode, b)
		}
		if _, err := s.DB.Exec(ctx, `UPDATE sessions SET expires_at=now()-interval '1 second'`); err != nil {
			t.Fatal(err)
		}
		expired, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		expired.Body.Close()
		if expired.StatusCode != 303 || !strings.Contains(expired.Header.Get("Location"), "return_to=") {
			t.Fatalf("expired session lost report: %d %s", expired.StatusCode, expired.Header.Get("Location"))
		}

	}
}
func TestReportReaderLabels(t *testing.T) {
	if reportTopic("fixed_income") != "Renda fixa" || reportState("await_data") != "Aguardando dados" || reportError("required_data_missing") != "Faltam dados para avaliar a proposta" || reportMandate("long_term") != "Longo prazo" {
		t.Fatal("reader labels missing")
	}
	a := New(nil, Config{Demo: true, Password: "demo-conselho", TelegramSecret: "synthetic"})
	d := autonomy.RunDetail{Run: autonomy.RunSummary{Topic: "fixed_income", Status: "await_data", ErrorCode: "required_data_missing"}, Tasks: []autonomy.TaskSummary{{Stage: 2, AgentID: "A17", Status: "complete"}}}
	w := httptest.NewRecorder()
	a.render(w, "reports.html", reportPageData(autonomy.ArchivePage{}, &d, ""))
	b := w.Body.String()
	for _, text := range []string{"Renda fixa", "Aguardando dados", "Faltam dados para avaliar a proposta", "Dante · Risco"} {
		if !strings.Contains(b, text) {
			t.Errorf("missing label %q", text)
		}
	}
	for _, raw := range []string{"fixed_income", "await_data", "required_data_missing"} {
		if strings.Contains(b, raw) {
			t.Errorf("internal code leaked %q", raw)
		}
	}
}
