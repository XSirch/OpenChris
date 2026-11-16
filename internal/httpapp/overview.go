package httpapp

import (
	"net/http"
	"time"
)

// Overview describes the available financial data without filling missing values.
type Overview struct {
	Mode                    string          `json:"mode"`
	ModeLabel               string          `json:"mode_label"`
	Currency                string          `json:"currency"`
	BankStatus              string          `json:"bank_status"`
	BankStatusText          string          `json:"bank_status_text"`
	CodexStatus             string          `json:"codex_status"`
	CodexStatusText         string          `json:"codex_status_text"`
	AsOf                    *string         `json:"as_of"`
	AsOfText                string          `json:"as_of_text"`
	NetWorthCents           *int64          `json:"net_worth_cents"`
	NetWorthText            string          `json:"net_worth_text"`
	AvailableCents          *int64          `json:"available_cents"`
	AvailableText           string          `json:"available_text"`
	ReserveMonths           *int            `json:"reserve_months"`
	ReserveText             string          `json:"reserve_text"`
	CommitmentText          string          `json:"commitment_text"`
	NextDecisionTitle       string          `json:"next_decision_title"`
	NextDecisionDescription string          `json:"next_decision_description"`
	Goals                   []OverviewGoal  `json:"goals"`
	Series                  []OverviewPoint `json:"series"`
}

type OverviewGoal struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	ProgressPercent int    `json:"progress_percent"`
	TargetText      string `json:"target_text"`
}

type OverviewPoint struct {
	Label      string `json:"label"`
	ValueCents int64  `json:"value_cents"`
}

func syntheticOverview() Overview {
	asOf, netWorth, available, reserve := "2026-10-26", int64(28435000), int64(2236000), 12
	return Overview{
		Mode:                    "synthetic",
		ModeLabel:               "Dados fictícios",
		Currency:                "BRL",
		BankStatus:              "not_connected",
		BankStatusText:          "Bancos não conectados",
		CodexStatus:             "not_connected",
		CodexStatusText:         "Codex não conectado",
		AsOf:                    &asOf,
		AsOfText:                "26 out 2026",
		NetWorthCents:           &netWorth,
		NetWorthText:            "R$ 284.350",
		AvailableCents:          &available,
		AvailableText:           "R$ 22.360",
		ReserveMonths:           &reserve,
		ReserveText:             "12 meses",
		CommitmentText:          "26 out",
		NextDecisionTitle:       "Aporte do mês",
		NextDecisionDescription: "Cenário demonstrativo",
		Goals: []OverviewGoal{
			{ID: "reserve", Title: "Reserva de emergência", ProgressPercent: 100, TargetText: "12 meses"},
		},
		Series: []OverviewPoint{
			{Label: "mai", ValueCents: 25100000},
			{Label: "jun", ValueCents: 25780000},
			{Label: "jul", ValueCents: 26620000},
			{Label: "ago", ValueCents: 27040000},
			{Label: "set", ValueCents: 27810000},
			{Label: "out", ValueCents: 28435000},
		},
	}
}

func manualOverview(summary FinanceSummary) Overview {
	overview := Overview{
		Mode:                    "manual",
		ModeLabel:               "Meus lançamentos",
		Currency:                "BRL",
		BankStatus:              "not_configured",
		BankStatusText:          "Bancos não configurados",
		CodexStatus:             "not_configured",
		CodexStatusText:         "IA não configurada",
		AsOfText:                "Não informado",
		NetWorthCents:           summary.NetWorthCents,
		NetWorthText:            summary.NetWorthText,
		AvailableText:           "Não informado",
		ReserveText:             "Não informada",
		CommitmentText:          "Não informado",
		NextDecisionTitle:       "Nenhuma decisão registrada",
		NextDecisionDescription: "",
		Goals:                   []OverviewGoal{},
		Series:                  []OverviewPoint{},
	}
	if summary.NetWorthDate != "" {
		overview.AsOf = &summary.NetWorthDate
		if date, err := time.Parse("2006-01-02", summary.NetWorthDate); err == nil {
			overview.AsOfText = date.Format("02/01/2006")
		}
	}
	if summary.Mode == "bank" {
		overview.Mode = "bank"
		overview.ModeLabel = "Dados bancários"
		overview.BankStatus = "connected"
		overview.BankStatusText = "Dados importados pelo Pluggy"
	}
	overview.AvailableCents = summary.AvailableToInvestCents
	if overview.AvailableCents != nil {
		overview.AvailableText = expenseMoney(*overview.AvailableCents)
	}
	if len(summary.FinancePendingItems) > 0 {
		overview.NextDecisionTitle = summary.FinancePendingItems[0].Title
		overview.NextDecisionDescription = summary.FinancePendingItems[0].Detail
	}
	return overview
}

func (a *App) overview(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorized(w, r, false); !ok {
		return
	}
	if a.config.Production {
		summary, err := a.financeSummary(r.Context(), "")
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "Não foi possível recuperar os indicadores financeiros.")
			return
		}
		respond(w, http.StatusOK, manualOverview(summary))
		return
	}
	respond(w, http.StatusOK, syntheticOverview())
}
