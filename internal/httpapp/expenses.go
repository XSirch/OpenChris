package httpapp

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"openchris/conselho/internal/finance"
	"openchris/conselho/internal/memory"
)

type ExpenseFilters struct {
	Source   string `json:"source"`
	Currency string `json:"currency"`
	Review   bool   `json:"review"`
	Month    string `json:"month"`
	Category string `json:"category"`
	Status   string `json:"status"`
	Query    string `json:"q"`
}

type ExpenseCategory struct {
	Color   string `json:"color"`
	Version int    `json:"version"`
	ID      string `json:"id"`
	Name    string `json:"name"`
}

type ExpenseCategoryTotal struct {
	ExpenseCategory
	TotalCents   int64  `json:"total_cents"`
	TotalText    string `json:"total_text"`
	SharePercent int    `json:"share_percent"`
	URL          string `json:"url"`
	Selected     bool   `json:"selected"`
}

type ExpenseTransaction struct {
	Source               string `json:"source"`
	Currency             string `json:"currency"`
	ClassificationSource string `json:"classification_source"`
	ReviewRequired       bool   `json:"review_required"`
	Version              int    `json:"version"`
	AmountUnknown        bool   `json:"amount_unknown"`
	ID                   string `json:"id"`
	Date                 string `json:"date"`
	DateText             string `json:"date_text"`
	Description          string `json:"description"`
	Category             string `json:"category"`
	CategoryName         string `json:"category_name"`
	Account              string `json:"account"`
	Kind                 string `json:"kind"`
	Status               string `json:"status"`
	StatusText           string `json:"status_text"`
	AmountCents          int64  `json:"amount_cents"`
	AmountText           string `json:"amount_text"`
	Recurring            bool   `json:"recurring"`
	RefundOf             string `json:"refund_of,omitempty"`
}

type ExpenseMonth struct {
	Month      string `json:"month"`
	Label      string `json:"label"`
	TotalCents *int64 `json:"total_cents"`
	TotalText  string `json:"total_text"`
	URL        string `json:"url"`
	Selected   bool   `json:"selected"`
}

type ExpenseReport struct {
	CurrencyOptions    []string               `json:"currency_options"`
	ReviewCount        int                    `json:"review_count"`
	RecurringCount     int                    `json:"recurring_count"`
	Mode               string                 `json:"mode"`
	ModeLabel          string                 `json:"mode_label"`
	Currency           string                 `json:"currency"`
	SourceLabel        string                 `json:"source_label"`
	AsOf               string                 `json:"as_of"`
	Filters            ExpenseFilters         `json:"filters"`
	MonthLabel         string                 `json:"month_label"`
	HasData            bool                   `json:"has_data"`
	Incomplete         bool                   `json:"incomplete"`
	TotalCents         *int64                 `json:"total_cents"`
	TotalText          string                 `json:"total_text"`
	PreviousTotalCents *int64                 `json:"previous_total_cents"`
	ComparisonText     string                 `json:"comparison_text"`
	PendingCents       int64                  `json:"pending_cents"`
	PendingText        string                 `json:"pending_text"`
	PendingCount       int                    `json:"pending_count"`
	RefundCents        int64                  `json:"refund_cents"`
	RefundText         string                 `json:"refund_text"`
	ExcludedCount      int                    `json:"excluded_count"`
	ResultCount        int                    `json:"result_count"`
	ResultCents        int64                  `json:"result_cents"`
	ResultText         string                 `json:"result_text"`
	Categories         []ExpenseCategoryTotal `json:"categories"`
	CategoryOptions    []ExpenseCategory      `json:"category_options"`
	Transactions       []ExpenseTransaction   `json:"transactions"`
	Months             []ExpenseMonth         `json:"months"`
	ClearURL           string                 `json:"clear_url"`
	ExportURL          string                 `json:"export_url"`
	Filtered           bool                   `json:"filtered"`
}

var expenseCategories = []ExpenseCategory{
	{ID: "moradia", Name: "Moradia", Color: "#8bcebb"}, {ID: "alimentacao", Name: "Alimentação", Color: "#8bcebb"}, {ID: "transporte", Name: "Transporte", Color: "#8bcebb"},
	{ID: "saude", Name: "Saúde", Color: "#8bcebb"}, {ID: "assinaturas", Name: "Assinaturas", Color: "#8bcebb"}, {ID: "educacao", Name: "Educação", Color: "#8bcebb"},
	{ID: "lazer", Name: "Lazer", Color: "#8bcebb"}, {ID: "outros", Name: "Compras e outros", Color: "#8bcebb"}, {ID: "sem-categoria", Name: "Sem categoria", Color: "#8bcebb"},
}

// This fixture is isolated from the canonical ledger and all persisted memory.
func syntheticExpenses() []ExpenseTransaction {
	rows := make([]ExpenseTransaction, 0, 40)
	for index, month := range []string{"2026-08", "2026-09", "2026-10"} {
		entries := []struct {
			day, description, category string
			cents                      int64
			recurring                  bool
		}{
			{"05", "Aluguel do apartamento", "moradia", 320000, true},
			{"08", "Mercado da semana", "alimentacao", []int64{81000, 79000, 84270}[index], false},
			{"12", "Restaurante do centro", "alimentacao", []int64{14300, 15650, 12850}[index], false},
			{"14", "Transporte por aplicativo", "transporte", []int64{20500, 17480, 18640}[index], false},
			{"10", "Plano de saúde", "saude", 62000, true},
			{"15", "Assinatura de streaming", "assinaturas", 5990, true},
			{"18", "Curso de idiomas", "educacao", 18900, true},
			{"20", "Cinema e lazer", "lazer", []int64{12000, 11000, 9800}[index], false},
			{"22", "Compra online", "outros", []int64{14990, 19990, 24990}[index], false},
		}
		for n, entry := range entries {
			rows = append(rows, ExpenseTransaction{ID: fmt.Sprintf("%s-%02d", month, n+1), Date: month + "-" + entry.day, Description: entry.description, Category: entry.category, Account: "Conta de exemplo", Kind: "expense", Status: "confirmed", AmountCents: entry.cents, Recurring: entry.recurring})
		}
		rows = append(rows,
			ExpenseTransaction{ID: month + "-transfer", Date: month + "-10", Description: "Transferência entre contas próprias", Kind: "transfer_internal", Status: "confirmed", AmountCents: 100000},
			ExpenseTransaction{ID: month + "-bill", Date: month + "-16", Description: "Pagamento de fatura já detalhada", Kind: "card_bill_payment", Status: "confirmed", AmountCents: 93540})
	}
	rows = append(rows,
		ExpenseTransaction{ID: "2026-10-refund", Date: "2026-10-24", Description: "Estorno parcial da compra online", Category: "outros", Account: "Conta de exemplo", Kind: "refund", Status: "confirmed", AmountCents: -4990, RefundOf: "2026-10-09"},
		ExpenseTransaction{ID: "2026-10-pending-1", Date: "2026-10-25", Description: "Farmácia do bairro", Category: "saude", Account: "Cartão de exemplo", Kind: "expense", Status: "pending", AmountCents: 8750},
		ExpenseTransaction{ID: "2026-10-pending-2", Date: "2026-10-26", Description: "Compra aguardando identificação", Category: "sem-categoria", Account: "Cartão de exemplo", Kind: "expense", Status: "pending", AmountCents: 12900})
	return rows
}

func expenseMoney(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	whole := strconv.FormatInt(cents/100, 10)
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "." + whole[i:]
	}
	return fmt.Sprintf("%sR$ %s,%02d", sign, whole, cents%100)
}

func expenseSearch(text string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case 'á', 'à', 'ã', 'â', 'ä':
			return 'a'
		case 'é', 'è', 'ê', 'ë':
			return 'e'
		case 'í', 'ì', 'î', 'ï':
			return 'i'
		case 'ó', 'ò', 'õ', 'ô', 'ö':
			return 'o'
		case 'ú', 'ù', 'û', 'ü':
			return 'u'
		case 'ç':
			return 'c'
		}
		return r
	}, strings.ToLower(text))
}

func expenseMonthLabel(month string) string {
	parsed, _ := time.Parse("2006-01", month)
	names := []string{"janeiro", "fevereiro", "março", "abril", "maio", "junho", "julho", "agosto", "setembro", "outubro", "novembro", "dezembro"}
	return fmt.Sprintf("%s de %d", names[int(parsed.Month())-1], parsed.Year())
}

func expenseURL(base string, filters ExpenseFilters) string {
	values := url.Values{"month": {filters.Month}}
	if filters.Currency != "" {
		values.Set("currency", filters.Currency)
	}
	if filters.Review {
		values.Set("review", "1")
	}
	if filters.Source != "" {
		values.Set("source", filters.Source)
	}
	if filters.Category != "" {
		values.Set("category", filters.Category)
	}
	if filters.Status != "all" {
		values.Set("status", filters.Status)
	}
	if filters.Query != "" {
		values.Set("q", filters.Query)
	}
	return base + "?" + values.Encode()
}

func buildExpenseReport(values url.Values) (ExpenseReport, error) {
	return buildExpenseReportFrom(values, syntheticExpenses(), expenseCategories, "demo")
}

func (a *App) expenseReport(ctx context.Context, values url.Values) (ExpenseReport, error) {
	source := values.Get("source")
	if len(values["source"]) > 1 {
		return ExpenseReport{}, errors.New("Escolha apenas uma origem para os lançamentos.")
	}
	if source == "demo" {
		if a.config.Production {
			return ExpenseReport{}, errors.New("Demonstrações estão indisponíveis neste ambiente.")
		}
		return buildExpenseReport(values)
	}
	if source != "" && source != "manual" && source != "bank" {
		return ExpenseReport{}, errors.New("Escolha Dados bancários ou Lançamentos manuais.")
	}
	if source == "" || source == "bank" {
		bankRows, err := finance.ReadBankTransactions(ctx, a.store.DB, memory.UserID, time.Now().UTC())
		if err != nil {
			return ExpenseReport{}, err
		}
		if source == "bank" || len(bankRows) > 0 {
			rows := make([]ExpenseTransaction, 0, len(bankRows))
			options := []ExpenseCategory{}
			seen := map[string]bool{}
			for _, row := range bankRows {
				rows = append(rows, ExpenseTransaction{ID: row.ID, Date: row.Date, Description: row.Description, Account: row.Account, Category: row.Category, CategoryName: row.CategoryName, Kind: row.Kind, Status: row.Status, AmountCents: row.AmountCents, AmountUnknown: !row.AmountKnown, Currency: row.Currency, Source: "pluggy", ClassificationSource: row.ClassificationSource, ReviewRequired: row.ReviewRequired, Version: row.Version, Recurring: row.Recurring})
				if !seen[row.Category] {
					seen[row.Category] = true
					options = append(options, ExpenseCategory{ID: row.Category, Name: row.CategoryName, Color: "#8bcebb"})
				}
			}
			sort.Slice(options, func(i, j int) bool { return options[i].Name < options[j].Name })
			return buildExpenseReportFrom(values, rows, options, "bank")
		}
	}
	store := finance.New(a.store.DB)
	categories, err := store.Categories(ctx, memory.UserID)
	if err != nil {
		return ExpenseReport{}, err
	}
	transactions, err := store.Transactions(ctx, memory.UserID)
	if err != nil {
		return ExpenseReport{}, err
	}
	options := make([]ExpenseCategory, 0, len(categories))
	rows := make([]ExpenseTransaction, 0, len(transactions))
	for _, category := range categories {
		options = append(options, ExpenseCategory{ID: category.ID, Name: category.Name, Color: category.Color, Version: category.Version})
	}
	for _, row := range transactions {
		rows = append(rows, ExpenseTransaction{ID: row.ID, Date: row.Date, Description: row.Description, Category: row.CategoryID, Account: row.Account, Kind: "expense", Status: row.Status, AmountCents: row.AmountCents, Recurring: row.Recurring, Source: "manual", Currency: "BRL"})
	}
	return buildExpenseReportFrom(values, rows, options, "manual")
}

func buildExpenseReportFrom(values url.Values, rows []ExpenseTransaction, categoryOptions []ExpenseCategory, source string) (ExpenseReport, error) {
	filters := ExpenseFilters{Source: source, Month: values.Get("month"), Category: values.Get("category"), Status: values.Get("status"), Query: strings.TrimSpace(values.Get("q"))}
	filters.Currency = values.Get("currency")
	filters.Review = values.Get("review") == "1"
	if len(values["review"]) > 1 || values.Get("review") != "" && values.Get("review") != "1" {
		return ExpenseReport{}, errors.New("Escolha uma revisão válida.")
	}
	if filters.Currency == "" {
		filters.Currency = "BRL"
	}
	if len(values["currency"]) > 1 || len(filters.Currency) != 3 || strings.IndexFunc(filters.Currency, func(r rune) bool { return r < 'A' || r > 'Z' }) >= 0 {
		return ExpenseReport{}, errors.New("Escolha uma moeda válida.")
	}
	if source != "bank" && filters.Currency != "BRL" {
		return ExpenseReport{}, errors.New("Lançamentos manuais e demonstrações usam reais.")
	}
	money := func(cents int64) string { return strings.Replace(expenseMoney(cents), "R$ ", filters.Currency+" ", 1) }
	if filters.Currency == "BRL" {
		money = expenseMoney
	}
	if filters.Month == "" {
		filters.Month = "2026-10"
		if source != "demo" {
			filters.Month = financeToday()[:7]
		}
	}
	date, err := time.Parse("2006-01", filters.Month)
	if err != nil || len(filters.Month) != 7 || date.Year() < 1900 || date.Year() > 2100 {
		return ExpenseReport{}, errors.New("Escolha um mês válido entre 1900 e 2100.")
	}
	if filters.Status == "" {
		filters.Status = "all"
	}
	if filters.Status != "all" && filters.Status != "confirmed" && filters.Status != "pending" {
		return ExpenseReport{}, errors.New("Escolha todas as situações, confirmados ou pendentes.")
	}
	categoryNames := map[string]string{}
	categoryMetadata := map[string]ExpenseCategory{}
	for _, category := range categoryOptions {
		categoryNames[category.ID] = category.Name
		categoryMetadata[category.ID] = category
	}
	if filters.Category != "" && categoryNames[filters.Category] == "" {
		return ExpenseReport{}, errors.New("Escolha uma categoria disponível.")
	}
	if !utf8.ValidString(filters.Query) || utf8.RuneCountInString(filters.Query) > 80 {
		return ExpenseReport{}, errors.New("A busca aceita até 80 caracteres em UTF-8.")
	}
	report := ExpenseReport{Mode: "synthetic", ModeLabel: "Dados fictícios", Currency: filters.Currency, SourceLabel: "Cenário fictício · bancos não conectados", AsOf: "2026-10-26", Filters: filters, MonthLabel: expenseMonthLabel(filters.Month), TotalText: "Sem dados", PendingText: "Sem dados", ComparisonText: "Sem dados do mês anterior para comparar.", Categories: []ExpenseCategoryTotal{}, Transactions: []ExpenseTransaction{}, Months: []ExpenseMonth{}, CategoryOptions: categoryOptions, ClearURL: expenseURL("/expenses", ExpenseFilters{Source: source, Currency: filters.Currency, Month: filters.Month, Status: "all"}), ExportURL: expenseURL("/expenses.csv", filters), Filtered: filters.Category != "" || filters.Status != "all" || filters.Query != ""}
	currencies := map[string]bool{"BRL": true}
	for _, row := range rows {
		if row.Currency != "" {
			currencies[row.Currency] = true
		}
	}
	for currency := range currencies {
		report.CurrencyOptions = append(report.CurrencyOptions, currency)
	}
	sort.Strings(report.CurrencyOptions)
	totals := map[string]int64{}
	knownTotals := map[string]int{}
	incomplete := map[string]bool{}
	coverage := map[string]bool{}
	categoryTotals := map[string]int64{}
	query := expenseSearch(filters.Query)
	for _, row := range rows {
		if row.Currency == "" && source != "bank" {
			row.Currency = "BRL"
		}
		currencyMatches := row.Currency == filters.Currency
		month := ""
		if len(row.Date) >= 10 {
			month = row.Date[:7]
		}
		if source == "bank" && month != "" && (currencyMatches || row.Currency == "") && (row.Kind == "unknown" || (row.Kind == "expense" || row.Kind == "refund") && (row.AmountUnknown || row.Currency == "")) {
			incomplete[month] = true
			coverage[month] = true
		}
		if !currencyMatches && !(filters.Review && row.ReviewRequired && row.Currency == "") {
			continue
		}
		if month != "" {
			coverage[month] = true
		}
		spending := (row.Kind == "expense" || row.Kind == "refund") && !row.AmountUnknown && currencyMatches && month != ""
		if !spending {
			if month == filters.Month {
				report.ExcludedCount++
			}
			if source != "bank" {
				continue
			}
		}
		if spending && row.Status == "confirmed" {
			totals[month] += row.AmountCents
			knownTotals[month]++
		}
		if month != filters.Month && !filters.Review {
			continue
		}
		row.CategoryName = categoryNames[row.Category]
		row.AmountText = money(row.AmountCents)
		if row.AmountUnknown {
			row.AmountText = "Não informado"
		} else if row.Currency == "" {
			row.AmountText = "Moeda não informada"
		}
		row.DateText = "Não informada"
		if len(row.Date) >= 10 {
			row.DateText = row.Date[8:10] + "/" + row.Date[5:7]
			if filters.Review {
				row.DateText += "/" + row.Date[:4]
			}
		}
		row.StatusText = "Confirmado"
		if row.Status == "pending" {
			row.StatusText = "Pendente"
			if spending && month == filters.Month {
				report.PendingCount++
				report.PendingCents += row.AmountCents
			}
		}
		if row.ReviewRequired {
			report.ReviewCount++
		}
		if spending && row.Recurring {
			report.RecurringCount++
		}
		if spending && row.Status == "confirmed" && month == filters.Month {
			categoryTotals[row.Category] += row.AmountCents
			if row.Kind == "refund" {
				report.RefundCents -= row.AmountCents
			}
		}
		if filters.Category != "" && row.Category != filters.Category {
			continue
		}
		if filters.Review && !row.ReviewRequired {
			continue
		}
		if filters.Status != "all" && row.Status != filters.Status {
			continue
		}
		if query != "" && !strings.Contains(expenseSearch(row.Description+" "+row.CategoryName+" "+row.Account), query) {
			continue
		}
		report.Transactions = append(report.Transactions, row)
		if spending {
			report.ResultCents += row.AmountCents
		}
	}
	monthTotal := func(month string) (*int64, string) {
		if incomplete[month] && knownTotals[month] == 0 {
			return nil, "Dados incompletos"
		}
		total := totals[month]
		label := money(total)
		if incomplete[month] {
			label += " (parcial)"
		}
		return &total, label
	}
	report.HasData = coverage[filters.Month]
	report.Incomplete = incomplete[filters.Month]
	if !report.HasData {
		report.ComparisonText = "Sem dados de gastos neste mês para comparar."
	}
	if report.HasData {
		report.TotalCents, report.TotalText = monthTotal(filters.Month)
		report.PendingText = money(report.PendingCents)
	}
	report.RefundText = money(report.RefundCents)
	previousMonth := date.AddDate(0, -1, 0).Format("2006-01")
	if coverage[previousMonth] {
		report.PreviousTotalCents, _ = monthTotal(previousMonth)
		previous := totals[previousMonth]
		if report.HasData && !report.Incomplete && !incomplete[previousMonth] {
			delta := totals[filters.Month] - previous
			report.ComparisonText = money(delta) + " em relação a " + expenseMonthLabel(previousMonth) + "."
			if previous > 0 {
				percent := float64(delta) * 100 / float64(previous)
				report.ComparisonText = strings.ReplaceAll(fmt.Sprintf("%+.1f%%", percent), ".", ",") + " em relação a " + expenseMonthLabel(previousMonth) + "."
			}
		}
	}
	if report.Incomplete || incomplete[previousMonth] {
		report.ComparisonText = "Há dados incompletos. Revise os lançamentos antes de comparar os meses."
	}
	positiveTotal := int64(0)
	for _, total := range categoryTotals {
		if total > 0 {
			positiveTotal += total
		}
	}
	for id, total := range categoryTotals {
		percent := 0
		if total > 0 && positiveTotal > 0 {
			percent = int((total*100 + positiveTotal/2) / positiveTotal)
		}
		next := filters
		next.Category = id
		report.Categories = append(report.Categories, ExpenseCategoryTotal{ExpenseCategory: categoryMetadata[id], TotalCents: total, TotalText: money(total), SharePercent: percent, URL: expenseURL("/expenses", next), Selected: id == filters.Category})
	}
	sort.Slice(report.Categories, func(i, j int) bool {
		a, b := report.Categories[i], report.Categories[j]
		if a.TotalCents == b.TotalCents {
			return a.ID < b.ID
		}
		return a.TotalCents > b.TotalCents
	})
	sort.Slice(report.Transactions, func(i, j int) bool {
		a, b := report.Transactions[i], report.Transactions[j]
		if a.Date == b.Date {
			return a.ID > b.ID
		}
		return a.Date > b.Date
	})
	report.ResultCount = len(report.Transactions)
	report.ResultText = money(report.ResultCents)
	months := []string{"2026-08", "2026-09", "2026-10"}
	if source != "demo" {
		months = []string{}
		for month := range coverage {
			months = append(months, month)
		}
		sort.Strings(months)
		if len(months) > 6 {
			months = months[len(months)-6:]
		}
	}
	for _, month := range months {
		next := filters
		next.Month = month
		total, label := monthTotal(month)
		report.Months = append(report.Months, ExpenseMonth{Month: month, Label: expenseMonthLabel(month), TotalCents: total, TotalText: label, URL: expenseURL("/expenses", next), Selected: month == filters.Month})
	}
	if source == "manual" {
		report.Mode = "manual"
		report.ModeLabel = "Meus lançamentos"
		report.SourceLabel = "Informado por você · sem conexão bancária"
		report.AsOf = financeToday()
	}
	if source == "bank" {
		report.Mode = "bank"
		report.ModeLabel = "Dados bancários"
		report.SourceLabel = "Pluggy · lançamentos manuais consultados separadamente"
		report.AsOf = financeToday()
	}
	return report, nil
}

func (a *App) expensesPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.session(r); !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	report, err := a.expenseReport(r.Context(), r.URL.Query())
	csrf, _ := a.session(r)
	data := map[string]any{"Production": a.config.Production, "ExpensePage": true, "FinanceProfilePage": false, "Report": report, "CSRF": csrf, "Today": financeToday(), "ExpenseKey": memory.ID(), "Saved": financeSavedText(r.URL.Query().Get("saved")), "Form": url.Values{}, "FormAction": ""}
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		data["Error"] = err.Error()
	}
	a.render(w, "expenses.html", data)
}

func (a *App) expensesAPI(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorized(w, r, false); !ok {
		return
	}
	report, err := a.expenseReport(r.Context(), r.URL.Query())
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	respond(w, 200, report)
}

func (a *App) expensesCSV(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorized(w, r, false); !ok {
		return
	}
	report, err := a.expenseReport(r.Context(), r.URL.Query())
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="gastos-%s.csv"`, report.Filters.Month))
	_, _ = w.Write([]byte{0xef, 0xbb, 0xbf})
	writer := csv.NewWriter(w)
	writer.Comma = ';'
	_ = writer.Write([]string{"Data", "Descrição", "Categoria", "Conta", "Tipo", "Situação", "Valor (" + report.Currency + ")", "Origem"})
	for _, row := range report.Transactions {
		amount := strings.ReplaceAll(strings.ReplaceAll(expenseMoney(row.AmountCents), "R$ ", ""), ".", "")
		if row.AmountUnknown || row.Currency != report.Currency {
			amount = ""
		}
		_ = writer.Write([]string{row.Date, csvExpenseText(row.Description), csvExpenseText(row.CategoryName), csvExpenseText(row.Account), row.Kind, row.StatusText, amount, report.ModeLabel})
	}
	writer.Flush()
}

func csvExpenseText(value string) string {
	if len(value) > 0 && strings.ContainsRune("=+-@\t\r", rune(value[0])) {
		return "'" + value
	}
	return value
}
