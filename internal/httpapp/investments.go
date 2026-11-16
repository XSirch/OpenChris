package httpapp

import (
	"fmt"
	"strings"

	"openchris/conselho/internal/pluggy"
)

func investmentStateText(state string) string {
	labels := map[string]string{"available": "Fonte sincronizada", "partial": "Cobertura parcial", "stale": "Coleta desatualizada", "disabled": "Pluggy desativado", "unavailable": "Dados indisponíveis", "awaiting_authorization": "Aguardando dados autorizados"}
	if label := labels[state]; label != "" {
		return label
	}
	return "Dados indisponíveis"
}
func investmentCoverageText(p pluggy.Portfolio) string {
	active := "ativas"
	if p.ActiveCount == 1 {
		active = "ativa"
	}
	withdrawn := "resgatadas"
	if p.WithdrawnCount == 1 {
		withdrawn = "resgatada"
	}
	parts := []string{fmt.Sprintf("%d %s", p.ActiveCount, active), fmt.Sprintf("%d %s", p.WithdrawnCount, withdrawn)}
	if p.UnknownCount > 0 {
		parts = append(parts, fmt.Sprintf("%d com estado desconhecido", p.UnknownCount))
	}
	if p.MissingValueCount > 0 {
		parts = append(parts, fmt.Sprintf("%d sem valor ou moeda", p.MissingValueCount))
	}
	if p.WithheldCount > 0 {
		parts = append(parts, fmt.Sprintf("%d fora do consentimento atual", p.WithheldCount))
	}
	return strings.Join(parts, " · ")
}
func investmentEmptyText(state string) string {
	if state == "available" || state == "partial" || state == "stale" {
		return "Nenhuma posição ativa reportada nesta coleta."
	}
	return "Consulte a autorização e a coleta em Conexões."
}
func investmentTypeText(kind string) string {
	labels := map[string]string{"FIXED_INCOME": "Renda fixa", "MUTUAL_FUND": "Fundo de investimento", "EQUITY": "Renda variável", "ETF": "ETF", "COE": "COE", "SECURITY": "Título", "OTHER": "Outros"}
	if label := labels[kind]; label != "" {
		return label
	}
	return kind
}
func investmentStatusText(status string) string {
	if status == "ACTIVE" {
		return "Ativo"
	}
	if status == "TOTAL_WITHDRAWAL" {
		return "Resgatado"
	}
	return "Estado não confirmado"
}
