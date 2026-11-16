package httpapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"openchris/conselho/internal/finance"
	"openchris/conselho/internal/memory"
	"openchris/conselho/internal/pluggy"
)

var errFinanceMonth = errors.New("Escolha um mês válido.")

type FinanceSummary struct {
	Cash                   []finance.CurrencyTotal `json:"cash"`
	AvailableToInvestCents *int64                  `json:"available_to_invest_cents"`
	FinancePendingItems    []finance.ActionItem    `json:"finance_pending_items"`
	PersonalRevision       string                  `json:"personal_revision"`
	Mode                   string                  `json:"mode"`
	Month                  string                  `json:"month"`
	MonthLabel             string                  `json:"month_label"`
	ExpenseCents           int64                   `json:"expense_cents"`
	ExpenseText            string                  `json:"expense_text"`
	ExpenseCount           int                     `json:"expense_count"`
	PendingCents           int64                   `json:"pending_cents"`
	PendingText            string                  `json:"pending_text"`
	PendingCount           int                     `json:"pending_count"`
	TopCategory            string                  `json:"top_category"`
	TopCategoryText        string                  `json:"top_category_text"`
	NetWorthCents          *int64                  `json:"net_worth_cents"`
	NetWorthText           string                  `json:"net_worth_text"`
	NetWorthDate           string                  `json:"net_worth_date"`
	AssetsText             string                  `json:"assets_text"`
	LiabilitiesText        string                  `json:"liabilities_text"`
	PendingJobs            int                     `json:"pending_jobs"`
	FailedJobs             int                     `json:"failed_jobs"`
	ActivityText           string                  `json:"activity_text"`
	LastActivity           *time.Time              `json:"last_activity"`
	SnapshotAt             string                  `json:"snapshot_at"`
	Investments            pluggy.Portfolio        `json:"investments"`
	PluggyWealth           *pluggyWealthCard       `json:"pluggy_wealth,omitempty"`
}

type pluggyWealthCard struct {
	pluggy.Wealth
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func bankWealthCard(wealth pluggy.Wealth, err error) *pluggyWealthCard {
	card := &pluggyWealthCard{Wealth: wealth, Status: "Atualizado"}
	if err != nil {
		card.Issues = []string{"A consulta ao patrimônio está indisponível. Tente novamente em instantes."}
	}
	switch {
	case wealth.Net == "":
		card.Net, card.Status = "Indisponível", "Sem total"
	case len(card.Issues) > 0:
		card.Status = "Parcial"
	}
	loc, _ := time.LoadLocation("America/Sao_Paulo")
	if !wealth.BankAsOf.IsZero() {
		card.Detail = "Atualização bancária mais antiga: " + wealth.BankAsOf.In(loc).Format("02/01/2006 às 15:04") + "."
	}
	if !wealth.LastImported.IsZero() {
		if card.Detail != "" {
			card.Detail += " "
		}
		card.Detail += "Última importação: " + wealth.LastImported.In(loc).Format("02/01/2006 às 15:04") + "."
	}
	return card
}

func (s FinanceSummary) WealthTitle() string {
	if s.PluggyWealth != nil {
		return "Patrimônio via Pluggy"
	}
	return "Patrimônio líquido manual"
}

func (s FinanceSummary) WealthValue() string {
	if s.PluggyWealth != nil {
		return s.PluggyWealth.Net
	}
	return s.NetWorthText
}

func (s FinanceSummary) ManualWealthDetail() string {
	if date, err := time.Parse("2006-01-02", s.NetWorthDate); err == nil {
		return "Referência manual: " + date.Format("02/01/2006") + "."
	}
	return "Informe ativos e dívidas para calcular."
}

func (a *App) financeSummary(ctx context.Context, month string) (FinanceSummary, error) {
	if month == "" {
		month = financeToday()[:7]
	}
	date, err := time.Parse("2006-01", month)
	if err != nil || len(month) != 7 || date.Year() < 1900 || date.Year() > 2100 {
		return FinanceSummary{}, errFinanceMonth
	}
	store := finance.New(a.store.DB)
	report, err := a.expenseReport(ctx, url.Values{"month": {month}})
	if err != nil {
		return FinanceSummary{}, err
	}
	wealth, err := store.LatestNetWorth(ctx, memory.UserID)
	if err != nil {
		return FinanceSummary{}, err
	}
	summary := FinanceSummary{Mode: "manual", Month: month, MonthLabel: expenseMonthLabel(month), ExpenseText: "Sem lançamentos", PendingText: "Nenhuma pendência", TopCategory: "Sem gastos confirmados", TopCategoryText: "", NetWorthText: "Não informado", AssetsText: "", LiabilitiesText: "", ActivityText: "Nenhuma tarefa pendente", SnapshotAt: time.Now().UTC().Format(time.RFC3339Nano)}
	summary.Mode = report.Mode
	summary.PendingCents = report.PendingCents
	summary.PendingCount = report.PendingCount
	if report.TotalCents != nil {
		summary.ExpenseCents = *report.TotalCents
	}
	for _, row := range report.Transactions {
		if !row.AmountUnknown && row.Status == "confirmed" && (row.Kind == "expense" || row.Kind == "refund") {
			summary.ExpenseCount++
		}
	}
	if summary.ExpenseCount > 0 {
		summary.ExpenseText = expenseMoney(summary.ExpenseCents)
	}
	if report.Incomplete {
		summary.ExpenseText = report.TotalText
	}
	if summary.PendingCount > 0 {
		summary.PendingText = expenseMoney(summary.PendingCents)
	}
	if len(report.Categories) > 0 && report.Categories[0].TotalCents > 0 {
		summary.TopCategory = report.Categories[0].Name
		summary.TopCategoryText = report.Categories[0].TotalText
	}
	personal, err := finance.ReadPersonalFinance(ctx, a.store.DB, memory.UserID, time.Now().UTC())
	if err != nil {
		return FinanceSummary{}, err
	}
	summary.Cash = personal.Cash
	summary.AvailableToInvestCents = personal.AvailableToInvestCents
	summary.FinancePendingItems = personal.PendingItems
	summary.PersonalRevision = personal.Revision
	if wealth != nil {
		summary.NetWorthCents = &wealth.NetCents
		summary.NetWorthText = expenseMoney(wealth.NetCents)
		summary.NetWorthDate = wealth.Date
		summary.AssetsText = moneyInputValue(wealth.AssetsCents)
		summary.LiabilitiesText = moneyInputValue(wealth.LiabilitiesCents)
	}
	summary.Investments, err = a.pluggyService.Portfolio(ctx)
	if err != nil {
		summary.Investments = pluggy.EmptyPortfolio("unavailable")
	}
	if a.config.Pluggy.Enabled {
		bank, bankErr := a.pluggyService.Wealth(ctx)
		summary.PluggyWealth = bankWealthCard(bank, bankErr)
	}
	if a.config.Production && !a.config.Codex.Enabled {
		summary.ActivityText = "IA não configurada"
		return summary, nil
	}
	if a.config.Production {
		err = a.store.DB.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status IN ('pending','running')),count(*) FILTER (WHERE status IN ('failed','failed_unknown','blocked','stale')),max(completed_at) FROM council_jobs j JOIN conversation_events e ON e.id=j.event_id WHERE e.user_id=$1`, memory.UserID).Scan(&summary.PendingJobs, &summary.FailedJobs, &summary.LastActivity)
	} else {
		err = a.store.DB.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status='pending'),count(*) FILTER (WHERE status='failed'),max(completed_at) FROM jobs j JOIN conversation_events e ON e.id=j.event_id WHERE e.user_id=$1`, memory.UserID).Scan(&summary.PendingJobs, &summary.FailedJobs, &summary.LastActivity)
	}
	if err != nil {
		return FinanceSummary{}, err
	}
	if summary.PendingJobs > 0 {
		summary.ActivityText = fmt.Sprintf("%d tarefa(s) na fila", summary.PendingJobs)
	} else if summary.FailedJobs > 0 {
		summary.ActivityText = fmt.Sprintf("%d tarefa(s) com falha", summary.FailedJobs)
	}
	return summary, nil
}

func moneyInputValue(cents int64) string { return fmt.Sprintf("%d,%02d", cents/100, cents%100) }
func (a *App) financeSummaryAPI(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorized(w, r, false); !ok {
		return
	}
	summary, err := a.financeSummary(r.Context(), r.URL.Query().Get("month"))
	if err != nil {
		if errors.Is(err, errFinanceMonth) {
			fail(w, 400, err.Error())
		} else {
			fail(w, 503, "Os indicadores estão indisponíveis. Tente novamente em instantes.")
		}
		return
	}
	respond(w, 200, summary)
}
func (a *App) financeEvents(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorized(w, r, false); !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, "Atualização ao vivo indisponível.")
		return
	}
	summary, err := a.financeSummary(r.Context(), r.URL.Query().Get("month"))
	if err != nil {
		if errors.Is(err, errFinanceMonth) {
			fail(w, 400, err.Error())
		} else {
			fail(w, 503, "Os indicadores estão indisponíveis. Tente novamente em instantes.")
		}
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	previous := ""
	beats := 0
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		stable := summary
		stable.SnapshotAt = ""
		signature, _ := json.Marshal(stable)
		if string(signature) != previous {
			payload, _ := json.Marshal(summary)
			if _, err = fmt.Fprintf(w, "event: finance.snapshot\ndata: %s\n\n", payload); err != nil {
				return
			}
			previous = string(signature)
			flusher.Flush()
		} else if beats%15 == 0 {
			if _, err = fmt.Fprint(w, ": conectado\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
		if r.URL.Query().Get("once") == "1" {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
		if _, valid := a.session(r); !valid {
			_, _ = fmt.Fprint(w, "event: session.expired\ndata: {}\n\n")
			flusher.Flush()
			return
		}
		summary, err = a.financeSummary(r.Context(), r.URL.Query().Get("month"))
		if err != nil {
			_, _ = fmt.Fprint(w, "event: finance.unavailable\ndata: {}\n\n")
			flusher.Flush()
			return
		}
		beats++
	}
}
