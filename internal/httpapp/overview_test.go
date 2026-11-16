package httpapp

import (
	"context"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openchris/conselho/assets"
)

func TestOverviewRequiresSessionAndKeepsSyntheticContract(t *testing.T) {
	s, server := fixture(t)
	if res := call(t, server, "GET", "/api/overview", "", nil, ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("overview without session: %d", res.StatusCode)
	}
	cookies, _ := login(t, server)
	res := call(t, server, "GET", "/api/overview", "", cookies, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("overview with session and no write token: %d", res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("overview content type: %q", got)
	}
	var result struct {
		Mode                    string `json:"mode"`
		ModeLabel               string `json:"mode_label"`
		Currency                string `json:"currency"`
		BankStatus              string `json:"bank_status"`
		BankStatusText          string `json:"bank_status_text"`
		CodexStatus             string `json:"codex_status"`
		CodexStatusText         string `json:"codex_status_text"`
		AsOf                    string `json:"as_of"`
		AsOfText                string `json:"as_of_text"`
		NetWorthCents           int64  `json:"net_worth_cents"`
		NetWorthText            string `json:"net_worth_text"`
		AvailableCents          int64  `json:"available_cents"`
		AvailableText           string `json:"available_text"`
		ReserveMonths           int    `json:"reserve_months"`
		ReserveText             string `json:"reserve_text"`
		CommitmentText          string `json:"commitment_text"`
		NextDecisionTitle       string `json:"next_decision_title"`
		NextDecisionDescription string `json:"next_decision_description"`
		Goals                   []struct {
			ID              string `json:"id"`
			Title           string `json:"title"`
			ProgressPercent int    `json:"progress_percent"`
			TargetText      string `json:"target_text"`
		} `json:"goals"`
		Series []struct {
			Label      string `json:"label"`
			ValueCents int64  `json:"value_cents"`
		} `json:"series"`
	}
	decoder := json.NewDecoder(res.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Mode != "synthetic" || result.ModeLabel != "Dados fictícios" || result.Currency != "BRL" {
		t.Fatalf("overview must identify the synthetic BRL fixture: %+v", result)
	}
	if result.BankStatus != "not_connected" || result.BankStatusText != "Bancos não conectados" || result.CodexStatus != "not_connected" || result.CodexStatusText != "Codex não conectado" {
		t.Fatalf("fixture claims a connected source: %+v", result)
	}
	if result.NetWorthCents != 28435000 || result.NetWorthText != "R$ 284.350" || result.AvailableCents != 2236000 || result.AvailableText != "R$ 22.360" {
		t.Fatalf("money must use integer cents and approved display values: %+v", result)
	}
	if result.AsOf != "2026-10-26" || result.AsOfText != "26 out 2026" || result.CommitmentText != "26 out" || result.ReserveMonths != 12 || result.ReserveText != "12 meses" {
		t.Fatalf("fixture dates and reserve changed: %+v", result)
	}
	if result.NextDecisionTitle != "Aporte do mês" || result.NextDecisionDescription != "Cenário demonstrativo" {
		t.Fatalf("decision must remain demonstrative: %+v", result)
	}
	if len(result.Goals) == 0 || len(result.Series) < 2 {
		t.Fatalf("fixture must contain goals and a money series: %+v", result)
	}
	for _, goal := range result.Goals {
		if goal.ID == "" || goal.Title == "" || goal.TargetText == "" || goal.ProgressPercent < 0 || goal.ProgressPercent > 100 {
			t.Fatalf("invalid goal projection: %+v", goal)
		}
	}
	for _, point := range result.Series {
		if point.Label == "" || point.ValueCents <= 0 {
			t.Fatalf("invalid money series: %+v", point)
		}
	}
	if result.Series[len(result.Series)-1].ValueCents != result.NetWorthCents {
		t.Fatal("series must end at the displayed synthetic net worth")
	}
	if _, err := s.DB.Exec(context.Background(), `UPDATE sessions SET expires_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if expired := call(t, server, "GET", "/api/overview", "", cookies, ""); expired.StatusCode != http.StatusUnauthorized {
		t.Fatalf("overview accepts expired session: %d", expired.StatusCode)
	}
}

func TestDashboardReceivesSyntheticOverview(t *testing.T) {
	s, server := fixture(t)
	cookies, _ := login(t, server)
	tpl := template.Must(template.New("dashboard.html").Parse(`{{.Overview.Mode}}|{{.Overview.NetWorthText}}|{{.Overview.AvailableText}}|{{.Overview.AsOfText}}|{{.Overview.CommitmentText}}|{{.Overview.NextDecisionTitle}}|{{.Overview.NextDecisionDescription}}|{{.Overview.BankStatus}}|{{.Overview.CodexStatus}}`))
	app := &App{store: s, templates: tpl}
	req := httptest.NewRequest("GET", "/dashboard", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	out := httptest.NewRecorder()
	app.dashboard(out, req)
	want := "synthetic|R$ 284.350|R$ 22.360|26 out 2026|26 out|Aporte do mês|Cenário demonstrativo|not_connected|not_connected"
	if out.Code != http.StatusOK || out.Body.String() != want {
		t.Fatalf("dashboard overview: %d %q", out.Code, out.Body.String())
	}
}

func TestOfficeAssetsAreServedFromEmbeddedFiles(t *testing.T) {
	_, server := fixture(t)
	want, err := assets.Files.ReadFile("office/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	res := call(t, server, "GET", "/static/office/manifest.json", "", nil, "")
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || string(body) != string(want) || !json.Valid(body) {
		t.Fatalf("embedded office manifest: %d %q", res.StatusCode, body)
	}
	if !strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") || res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unsafe office asset headers: %v", res.Header)
	}
	if missing := call(t, server, "GET", "/static/office/missing.json", "", nil, ""); missing.StatusCode != http.StatusNotFound {
		t.Fatalf("missing office asset: %d", missing.StatusCode)
	}
	if old := call(t, server, "GET", "/static/app.js", "", nil, ""); old.StatusCode != http.StatusOK {
		t.Fatalf("existing static route changed: %d", old.StatusCode)
	}
}
