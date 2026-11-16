package httpapp

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestExpenseReportSeparatesConfirmedRefundsPendingAndTransfers(t *testing.T) {
	report, err := buildExpenseReport(url.Values{"month": {"2026-10"}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Mode != "synthetic" || report.Currency != "BRL" || report.SourceLabel != "Cenário fictício · bancos não conectados" {
		t.Fatalf("missing source: %+v", report)
	}
	if !report.HasData || report.TotalCents == nil || *report.TotalCents != 552450 {
		t.Fatalf("confirmed net total: %v", report.TotalCents)
	}
	if report.PendingCents != 21650 || report.PendingCount != 2 || report.RefundCents != 4990 || report.ExcludedCount != 2 {
		t.Fatalf("reconciliation: %+v", report)
	}
	if report.PreviousTotalCents == nil || *report.PreviousTotalCents != 550010 {
		t.Fatalf("previous month: %v", report.PreviousTotalCents)
	}
	var categoryTotal int64
	for _, category := range report.Categories {
		categoryTotal += category.TotalCents
		if category.SharePercent < 0 || category.SharePercent > 100 {
			t.Fatal("invalid category share")
		}
	}
	if categoryTotal != *report.TotalCents {
		t.Fatalf("categories %d != total %d", categoryTotal, *report.TotalCents)
	}
	if report.ResultCount != 12 {
		t.Fatalf("visible expense/refund records: %d", report.ResultCount)
	}
	for _, row := range report.Transactions {
		if row.Kind != "expense" && row.Kind != "refund" {
			t.Fatalf("not an expense: %+v", row)
		}
	}
}

func TestExpenseFiltersComposeWithoutChangingMonthTotals(t *testing.T) {
	report, err := buildExpenseReport(url.Values{"month": {"2026-10"}, "category": {"saude"}, "status": {"pending"}, "q": {"FARMACIA"}})
	if err != nil {
		t.Fatal(err)
	}
	if report.ResultCount != 1 || report.Transactions[0].Description != "Farmácia do bairro" || report.ResultCents != 8750 {
		t.Fatalf("combined filters: %+v", report.Transactions)
	}
	if *report.TotalCents != 552450 || report.PendingCents != 21650 {
		t.Fatal("table filters must not silently change monthly summaries")
	}
	if !strings.Contains(report.ExportURL, "category=saude") || !strings.Contains(report.ExportURL, "status=pending") {
		t.Fatalf("CSV lost filters: %s", report.ExportURL)
	}
	for _, category := range report.Categories {
		u, _ := url.Parse(category.URL)
		if u.Query().Get("month") != "2026-10" || u.Query().Get("q") != "FARMACIA" {
			t.Fatalf("category link lost context: %s", category.URL)
		}
	}
}

func TestExpenseReportDistinguishesNoCoverageFromNoMatches(t *testing.T) {
	empty, err := buildExpenseReport(url.Values{"month": {"2026-07"}})
	if err != nil {
		t.Fatal(err)
	}
	if empty.HasData || empty.TotalCents != nil || empty.TotalText != "Sem dados" || len(empty.Transactions) != 0 {
		t.Fatalf("no data must not imply a zero balance: %+v", empty)
	}
	filtered, err := buildExpenseReport(url.Values{"q": {"inexistente"}})
	if err != nil {
		t.Fatal(err)
	}
	if !filtered.HasData || filtered.TotalCents == nil || filtered.ResultCount != 0 {
		t.Fatal("no matches confused with absent month")
	}
	future, err := buildExpenseReport(url.Values{"month": {"2026-11"}})
	if err != nil || future.ComparisonText != "Sem dados de gastos neste mês para comparar." {
		t.Fatal("missing current coverage described as missing previous coverage")
	}
	first, err := buildExpenseReport(url.Values{"month": {"2026-08"}})
	if err != nil || first.PreviousTotalCents != nil {
		t.Fatal("previous month must not be invented")
	}
}

func TestExpenseReportRejectsInvalidFilters(t *testing.T) {
	for _, values := range []url.Values{
		{"month": {"2026-13"}}, {"month": {"2026-2"}}, {"category": {"inventada"}}, {"status": {"completed"}}, {"q": {strings.Repeat("a", 81)}},
	} {
		if _, err := buildExpenseReport(values); err == nil {
			t.Fatalf("accepted %v", values)
		}
	}
}

func TestExpenseRoutesRequireSessionKeepFiltersAndDoNotWrite(t *testing.T) {
	store, server := fixture(t)
	for _, path := range []string{"/api/expenses", "/expenses.csv"} {
		if res := call(t, server, "GET", path, "", nil, ""); res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauthorized %s: %d", path, res.StatusCode)
		}
	}
	cookies, _ := login(t, server)
	before, err := store.Snapshot(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	result := call(t, server, "GET", "/api/expenses?source=demo&month=2026-10&category=outros&status=confirmed", "", cookies, "")
	if result.StatusCode != 200 {
		t.Fatalf("JSON status %d", result.StatusCode)
	}
	var report ExpenseReport
	if err := json.NewDecoder(result.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	if report.ResultCount != 2 || report.ResultCents != 20000 {
		t.Fatalf("refund filter: %+v", report.Transactions)
	}
	csvResult := call(t, server, "GET", "/expenses.csv?source=demo&month=2026-10&category=outros&status=confirmed", "", cookies, "")
	if csvResult.StatusCode != 200 || !strings.Contains(csvResult.Header.Get("Content-Disposition"), "gastos-2026-10.csv") || !strings.Contains(csvResult.Header.Get("Cache-Control"), "no-store") {
		t.Fatalf("export: %d %v", csvResult.StatusCode, csvResult.Header)
	}
	body, _ := io.ReadAll(csvResult.Body)
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(body), "\ufeff")))
	reader.Comma = ';'
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || !strings.Contains(string(body), "-49,90") || !strings.Contains(string(body), "Dados fictícios") {
		t.Fatalf("export does not match filtered table: %s", body)
	}
	for _, path := range []string{"/api/expenses?source=demo&month=2026-99", "/expenses.csv?source=demo&category=invalid"} {
		if res := call(t, server, "GET", path, "", cookies, ""); res.StatusCode != 400 {
			t.Fatalf("invalid filter: %d", res.StatusCode)
		}
	}
	after, err := store.Snapshot(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("financial preview mutated persisted memory")
	}
	if _, err := store.DB.Exec(context.Background(), `UPDATE sessions SET expires_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/expenses", "/expenses.csv"} {
		if res := call(t, server, "GET", path, "", cookies, ""); res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expired session accepted at %s", path)
		}
	}
	expiredPage := call(t, server, "GET", "/expenses", "", cookies, "")
	if expiredPage.Request.URL.Path != "/login" {
		t.Fatal("expired page session did not return to login")
	}
}

func TestExpensesPageRendersWithoutJavaScriptAndEscapesSearch(t *testing.T) {
	_, server := fixture(t)
	cookies, _ := login(t, server)
	res := call(t, server, "GET", "/expenses?source=demo&month=2026-10&q="+url.QueryEscape("<script>"), "", cookies, "")
	body, _ := io.ReadAll(res.Body)
	text := string(body)
	if res.StatusCode != 200 {
		t.Fatalf("page status %d", res.StatusCode)
	}
	for _, want := range []string{"Histórico de gastos", "Gastos por categoria", "Dados fictícios", "method=\"get\"", "name=\"month\"", "name=\"category\"", "name=\"status\"", "&lt;script&gt;", "Nenhum lançamento com esses filtros"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Contains(text, "<script>") {
		t.Fatal("query became markup")
	}
	dashboard := call(t, server, "GET", "/dashboard", "", cookies, "")
	b, _ := io.ReadAll(dashboard.Body)
	if !strings.Contains(string(b), "href=\"/expenses\"") {
		t.Fatal("dashboard lacks expense navigation")
	}
}
