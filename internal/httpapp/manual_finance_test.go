package httpapp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"openchris/conselho/internal/finance"
	"openchris/conselho/internal/memory"
	"openchris/conselho/migrations"
)

func manualFixture(t *testing.T) (*memory.Store, *httptest.Server) {
	s, server := fixture(t)
	if _, err := s.DB.Exec(context.Background(), `TRUNCATE finance_audit,finance_transactions,finance_net_worth,finance_categories RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	sql, err := migrations.Files.ReadFile("002_finance.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(context.Background(), string(sql)); err != nil {
		t.Fatal(err)
	}
	return s, server
}
func financeForm(t *testing.T, server *httptest.Server, values url.Values, cookies []*http.Cookie, csrf string) *http.Response {
	t.Helper()
	values.Set("csrf", csrf)
	req, _ := http.NewRequest("POST", server.URL+"/expenses/actions", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", server.URL)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}
func TestManualMoneyParsingKeepsExactIntegerCents(t *testing.T) {
	for input, want := range map[string]int64{"0": 0, "10,50": 1050, "1.234,56": 123456, "1234.56": 123456, " 12,3 ": 1230, "10000000000,00": 1000000000000} {
		got, err := parseMoneyBRL(input)
		if err != nil || got != want {
			t.Fatalf("%q -> %d %v", input, got, err)
		}
	}
	for _, input := range []string{"-1", "1e3", "1,234", "1.234.56", "NaN", "", "10000000000,01"} {
		if _, err := parseMoneyBRL(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}
func TestPersonalAndDemoSourcesStaySeparate(t *testing.T) {
	_, server := manualFixture(t)
	cookies, _ := login(t, server)
	for _, path := range []string{"/expenses", "/api/expenses", "/api/finance/summary"} {
		res := call(t, server, "GET", path, "", cookies, "")
		if res.StatusCode != 200 {
			t.Fatalf("%s: %d", path, res.StatusCode)
		}
	}
	personal := call(t, server, "GET", "/api/expenses?month=2026-10", "", cookies, "")
	var p ExpenseReport
	json.NewDecoder(personal.Body).Decode(&p)
	if p.Mode != "manual" || p.HasData || p.TotalCents != nil || len(p.Transactions) != 0 {
		t.Fatalf("personal starts with fake data: %+v", p)
	}
	demo := call(t, server, "GET", "/api/expenses?source=demo&month=2026-10", "", cookies, "")
	var d ExpenseReport
	json.NewDecoder(demo.Body).Decode(&d)
	if d.Mode != "synthetic" || d.TotalCents == nil || *d.TotalCents != 552450 {
		t.Fatal("explicit demo source changed")
	}
	summary := call(t, server, "GET", "/api/finance/summary", "", cookies, "")
	var live FinanceSummary
	json.NewDecoder(summary.Body).Decode(&live)
	if live.NetWorthCents != nil || live.NetWorthText != "Não informado" {
		t.Fatal("unknown wealth was turned into zero/demo money")
	}
	if res := call(t, server, "GET", "/api/expenses?source=unknown", "", cookies, ""); res.StatusCode != 400 {
		t.Fatal("invalid source accepted")
	}
}
func TestManualFormsPersistCategoriesExpensesAndWealthWithoutWritingMemory(t *testing.T) {
	s, server := manualFixture(t)
	cookies, csrf := login(t, server)
	before, _ := s.Snapshot(context.Background(), "")
	categoryValues := url.Values{"action": {"category-create"}, "name": {"Mercado doméstico"}, "color": {"#8bcebb"}}
	denied := financeForm(t, server, categoryValues, cookies, "")
	if denied.StatusCode != 403 {
		t.Fatalf("missing CSRF: %d", denied.StatusCode)
	}
	created := financeForm(t, server, categoryValues, cookies, csrf)
	if created.StatusCode != 201 {
		b, _ := io.ReadAll(created.Body)
		t.Fatalf("category: %d %s", created.StatusCode, b)
	}
	var out struct {
		Item finance.Category `json:"item"`
	}
	json.NewDecoder(created.Body).Decode(&out)
	expense := url.Values{"action": {"expense-create"}, "description": {"Compra privada de teste"}, "date": {"2026-10-27"}, "amount": {"100,50"}, "category_id": {out.Item.ID}, "account": {"Carteira manual"}, "status": {"confirmed"}, "idempotency_key": {"test-http-manual-0001"}}
	for i := 0; i < 2; i++ {
		res := financeForm(t, server, expense, cookies, csrf)
		if res.StatusCode != 201 {
			b, _ := io.ReadAll(res.Body)
			t.Fatalf("expense: %d %s", res.StatusCode, b)
		}
	}
	renamed := financeForm(t, server, url.Values{"action": {"category-update"}, "id": {out.Item.ID}, "name": {"Alimentação da casa"}, "color": {"#efbd72"}, "version": {"1"}}, cookies, csrf)
	if renamed.StatusCode != 200 {
		t.Fatalf("rename %d", renamed.StatusCode)
	}
	conflict := financeForm(t, server, url.Values{"action": {"category-update"}, "id": {out.Item.ID}, "name": {"Versão atrasada"}, "color": {"#efbd72"}, "version": {"1"}}, cookies, csrf)
	if conflict.StatusCode != 409 {
		t.Fatalf("stale category update: %d", conflict.StatusCode)
	}
	res := call(t, server, "GET", "/api/expenses?month=2026-10", "", cookies, "")
	var report ExpenseReport
	json.NewDecoder(res.Body).Decode(&report)
	if report.ResultCount != 1 || report.TotalCents == nil || *report.TotalCents != 10050 || report.Transactions[0].CategoryName != "Alimentação da casa" {
		t.Fatalf("lost identity or duplicate expense: %+v", report)
	}
	wealth := financeForm(t, server, url.Values{"action": {"net-worth"}, "assets": {"1000,00"}, "liabilities": {"1200,00"}, "date": {"2026-10-27"}, "idempotency_key": {"test-http-wealth-0001"}}, cookies, csrf)
	if wealth.StatusCode != 201 {
		b, _ := io.ReadAll(wealth.Body)
		t.Fatalf("wealth %d %s", wealth.StatusCode, b)
	}
	liveRes := call(t, server, "GET", "/api/finance/summary?month=2026-10", "", cookies, "")
	var live FinanceSummary
	json.NewDecoder(liveRes.Body).Decode(&live)
	if live.NetWorthCents == nil || *live.NetWorthCents != -20000 || live.ExpenseCents != 10050 || live.TopCategory != "Alimentação da casa" {
		t.Fatalf("live summary: %+v", live)
	}
	events := call(t, server, "GET", "/api/finance/events?once=1&month=2026-10", "", cookies, "")
	body, _ := io.ReadAll(events.Body)
	if events.StatusCode != 200 || !strings.Contains(string(body), "event: finance.snapshot") || strings.Contains(string(body), "Compra privada de teste") || strings.Contains(string(body), "Carteira manual") {
		t.Fatalf("unsafe SSE: %d %s", events.StatusCode, body)
	}
	after, _ := s.Snapshot(context.Background(), "")
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("manual finance changed memory/jobs/events")
	}
}

func TestManualCSVCategoryNamesAreSpreadsheetSafe(t *testing.T) {
	s, server := manualFixture(t)
	cookies, csrf := login(t, server)
	category, err := finance.New(s.DB).CreateCategory(context.Background(), memory.UserID, finance.CategoryInput{Name: "=1+1", Color: "#8bcebb"})
	if err != nil {
		t.Fatal(err)
	}
	res := financeForm(t, server, url.Values{"action": {"expense-create"}, "description": {"Teste CSV"}, "date": {"2026-10-27"}, "amount": {"1,00"}, "category_id": {category.ID}, "account": {"Conta QA"}, "status": {"confirmed"}, "idempotency_key": {"csv-category-qa-001"}}, cookies, csrf)
	if res.StatusCode != 201 {
		t.Fatalf("create expense %d", res.StatusCode)
	}
	out := call(t, server, "GET", "/expenses.csv?month=2026-10", "", cookies, "")
	body, _ := io.ReadAll(out.Body)
	if !strings.Contains(string(body), ";'=1+1;") {
		t.Fatal("category became an executable spreadsheet formula")
	}
}

func TestNativeCategoryFailurePreservesEditsAndExpenseKey(t *testing.T) {
	s, server := manualFixture(t)
	cookies, csrf := login(t, server)
	category, err := finance.New(s.DB).CreateCategory(context.Background(), memory.UserID, finance.CategoryInput{Name: "Curso", Color: "#8bcebb"})
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{"csrf": {csrf}, "action": {"category-update"}, "id": {category.ID}, "name": {"Moradia"}, "color": {"#efbd72"}, "version": {"1"}}
	req, _ := http.NewRequest("POST", server.URL+"/expenses/actions", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", server.URL)
	req.Header.Set("Accept", "text/html")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	text := string(body)
	if res.StatusCode != 409 {
		t.Fatalf("native conflict %d", res.StatusCode)
	}
	key := regexp.MustCompile(`name="idempotency_key" value="([^"]+)"`).FindStringSubmatch(text)
	if len(key) != 2 {
		t.Fatal("category validation removed the expense submission key")
	}
	expected := `id="category-name-` + category.ID + `" name="name" maxlength="60" value="Moradia"`
	if !strings.Contains(text, expected) {
		t.Fatal("native category error discarded the attempted name")
	}
}

func TestNativeStaleCategoryRetryCannotOverwriteNewerVersion(t *testing.T) {
	s, server := manualFixture(t)
	cookies, csrf := login(t, server)
	store := finance.New(s.DB)
	category, err := store.CreateCategory(context.Background(), memory.UserID, finance.CategoryInput{Name: "Curso", Color: "#8bcebb"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.UpdateCategory(context.Background(), memory.UserID, category.ID, finance.CategoryInput{Name: "Curso novo", Color: "#efbd72", Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{"csrf": {csrf}, "action": {"category-update"}, "id": {category.ID}, "name": {"Minha edição antiga"}, "color": {"#8bcebb"}, "version": {"1"}}
	for attempt := 0; attempt < 2; attempt++ {
		req, _ := http.NewRequest("POST", server.URL+"/expenses/actions", strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", server.URL)
		req.Header.Set("Accept", "text/html")
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 409 {
			t.Fatalf("attempt %d silently overwrote category: %d", attempt, res.StatusCode)
		}
		pattern := `name="id" value="` + category.ID + `"><input type="hidden" name="version" value="([0-9]+)"`
		version := regexp.MustCompile(pattern).FindStringSubmatch(string(body))
		if len(version) != 2 {
			t.Fatal("category version missing")
		}
		values.Set("version", version[1])
	}
	categories, _ := store.Categories(context.Background(), memory.UserID)
	for _, current := range categories {
		if current.ID == category.ID && (current.Name != "Curso novo" || current.Version != 2) {
			t.Fatal("newer category changed during retry")
		}
	}
}
