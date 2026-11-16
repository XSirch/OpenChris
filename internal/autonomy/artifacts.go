package autonomy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"openchris/conselho/internal/codex"
	"openchris/conselho/internal/finance"
	"openchris/conselho/internal/memory"
)

func (s *Service) persistStrategy(ctx context.Context, tx pgx.Tx, item task) error {
	var raw, critique []byte
	if err := tx.QueryRow(ctx, `SELECT result FROM autonomy_tasks WHERE run_id=$1 AND stage=1 AND status='complete'`, item.RoundID).Scan(&raw); err != nil {
		return err
	}
	var result codex.Result
	if json.Unmarshal(raw, &result) != nil || result.Strategy == nil {
		return &codex.Error{Kind: "invalid_output"}
	}
	draft := result.Strategy
	if strings.TrimSpace(draft.Title) == "" || strings.TrimSpace(draft.Hypothesis) == "" {
		return &codex.Error{Kind: "invalid_output"}
	}
	switch draft.Mandate {
	case "long_term", "tactical", "cross_mandate", "not_applicable":
	default:
		return &codex.Error{Kind: "invalid_output"}
	}
	if err := tx.QueryRow(ctx, `SELECT result FROM autonomy_tasks WHERE run_id=$1 AND stage=3 AND status='complete'`, item.RoundID).Scan(&critique); err != nil {
		return err
	}
	key := digest(strings.ToLower(strings.Join(strings.Fields(draft.Title), " ")) + "|" + draft.Mandate)
	id := memory.ID()
	version := 1
	err := tx.QueryRow(ctx, `INSERT INTO autonomy_strategies(id,user_id,identity_key,current_version,created_at) VALUES($1,$2,$3,1,$4) ON CONFLICT(user_id,identity_key) DO UPDATE SET current_version=autonomy_strategies.current_version+1 RETURNING id::text,current_version`, id, memory.UserID, key, s.now()).Scan(&id, &version)
	if err != nil {
		return err
	}
	evidence := []map[string]any{}
	var contextData struct {
		Sources []struct {
			ID         string    `json:"id"`
			Type       string    `json:"type"`
			ObservedAt time.Time `json:"observed_at"`
			URI        *string   `json:"uri"`
		} `json:"sources"`
	}
	var snapshot []byte
	if err = tx.QueryRow(ctx, `SELECT context FROM autonomy_tasks WHERE run_id=$1 AND stage=1`, item.RoundID).Scan(&snapshot); err != nil {
		return err
	}
	if json.Unmarshal(snapshot, &contextData) != nil {
		return &codex.Error{Kind: "invalid_output"}
	}
	for _, ref := range draft.Evidence {
		for _, source := range contextData.Sources {
			if source.ID != ref {
				continue
			}
			kind := source.Type
			switch kind {
			case "user", "financial", "official_law", "market", "agent", "document":
			default:
				kind = "document"
			}
			at := source.ObservedAt
			if at.IsZero() {
				at = item.Cutoff
			}
			evidence = append(evidence, map[string]any{"source_id": ref, "source_type": kind, "observed_at": at, "uri": source.URI, "valid_at": nil})
		}
	}
	artifact := map[string]any{"strategy_id": id, "schema_version": "strategy.v1", "title": draft.Title, "version": version, "mandate": draft.Mandate, "status": "research", "hypothesis": draft.Hypothesis, "time_horizon": draft.TimeHorizon, "conditions": list(draft.Conditions), "invalidation_conditions": list(draft.InvalidationConditions), "eligible_instruments": list(draft.EligibleInstruments), "risks": list(draft.Risks), "evidence": evidence, "legal_finding_refs": []string{}, "review_due_at": s.now().Add(6 * time.Hour), "last_reviewed_at": s.now(), "source_case_id": item.RoundID}
	encoded, err := json.Marshal(artifact)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO autonomy_strategy_versions(strategy_id,version,run_id,artifact,critique,created_at) VALUES($1,$2,$3,$4,$5,$6)`, id, version, item.RoundID, encoded, critique, s.now())
	return err
}
func list(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func (s *Service) createNotices(ctx context.Context, tx pgx.Tx, at time.Time) error {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return err
	}
	local := at.In(loc)
	hour := local.Hour()
	if hour < 8 || hour >= 22 {
		return nil
	}
	var paused bool
	var epoch time.Time
	if err = tx.QueryRow(ctx, `SELECT paused,notice_epoch FROM autonomy_state WHERE user_id=$1`, memory.UserID).Scan(&paused, &epoch); err != nil {
		return err
	}
	if paused {
		return nil
	}
	day := local.Format("2006-01-02")
	var exists bool
	var renewalID string
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM autonomy_notices WHERE user_id=$1 AND kind='daily' AND slot_key=$2)`, memory.UserID, day).Scan(&exists); err != nil {
		return err
	}
	if exists {
		// Only a digest proved unsent and invalidated solely by a financial
		// change can be rebuilt. Revocation and uncertain sends stay terminal.
		err = tx.QueryRow(ctx, `SELECT n.id::text FROM autonomy_notices n WHERE n.user_id=$1 AND n.kind='daily' AND n.slot_key=$2 AND n.delivery_state='cancelled' AND n.cancellation_code='financial_context_changed' AND n.created_at>=$3 AND n.created_at>=coalesce((SELECT valid_from FROM telegram_autonomy_identity WHERE user_id=$1),'-infinity'::timestamptz) AND NOT EXISTS(SELECT 1 FROM telegram_outbox o WHERE o.notification_id=n.id AND (o.status<>'failed' OR o.error_code IS DISTINCT FROM 'financial_context_changed')) FOR UPDATE`, memory.UserID, day, epoch).Scan(&renewalID)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
	}
	personal, err := finance.ReadPersonalFinance(ctx, tx, memory.UserID, at)
	if err != nil {
		return err
	}
	body := fmt.Sprintf("CONSELHO · resumo de %s às %s\n", local.Format("02/01/2006"), local.Format("15:04"))
	if personal.LastSyncedAt != nil {
		body += "Dados bancários consultados em " + personal.LastSyncedAt.In(loc).Format("02/01 às 15:04") + ".\n"
	}
	if len(personal.Expenses) == 0 {
		body += "Gastos do mês: sem lançamentos bancários confirmados.\n"
	}
	for i, total := range personal.Expenses {
		if i == 4 {
			body += "Gastos em outras moedas estão no relatório.\n"
			break
		}
		body += fmt.Sprintf("Gastos do mês (%s): %s.\n", total.Currency, digestMoney(total.AmountCents))
	}
	for i, total := range personal.Cash {
		if i == 4 {
			body += "Saldos em outras moedas estão no relatório.\n"
			break
		}
		body += fmt.Sprintf("Saldo observado (%s): %s.\n", total.Currency, digestMoney(total.AmountCents))
	}
	if personal.AvailableToInvestCents != nil {
		body += "Disponível para avaliar investimentos (BRL): " + digestMoney(*personal.AvailableToInvestCents) + ".\n"
	} else {
		body += "Valor disponível para investir: depende das pendências abaixo.\n"
	}
	for i, pending := range personal.PendingItems {
		if i == 3 {
			body += fmt.Sprintf("Mais %d pendência(s) no painel.\n", len(personal.PendingItems)-i)
			break
		}
		body += "• " + pending.Title + ".\n"
	}
	var runID, state, runFingerprint string
	var runAt time.Time
	err = tx.QueryRow(ctx, `SELECT id::text,status,fingerprint,created_at FROM autonomy_runs WHERE user_id=$1 AND topic='personal_finance' ORDER BY created_at DESC LIMIT 1`, memory.UserID).Scan(&runID, &state, &runFingerprint, &runAt)
	if err != nil && err != pgx.ErrNoRows {
		return err
	}
	currentFingerprint, err := s.builder.Fingerprint(ctx, tx, at)
	if err != nil {
		return err
	}
	if runID != "" && (runFingerprint != currentFingerprint || at.Sub(runAt) > 24*time.Hour) {
		body += "O relatório de " + runAt.In(loc).Format("02/01 às 15:04") + " é histórico e está desatualizado. Aguarde uma nova análise pessoal."
	} else {
		switch state {
		case "complete":
			body += "Análise pessoal concluída; consulte as sugestões e fontes no relatório."
		case "await_data":
			body += "O parecer está disponível, com dados ou evidências ainda necessários."
		case "pending", "running":
			body += "A análise pessoal ainda não foi concluída."
		case "partial", "stale":
			body += "A análise pessoal não concluiu uma recomendação válida. Consulte a pendência no relatório."
		default:
			body += "Ainda não há análise pessoal concluída."
		}
	}
	var run any
	if runID != "" {
		run = runID
	}
	_, err = tx.Exec(ctx, `INSERT INTO autonomy_notices(id,user_id,run_id,kind,slot_key,created_at,body,financial_revision) VALUES($1,$2,$3,'daily',$4,$5,$6,$7) ON CONFLICT(user_id,kind,slot_key) DO UPDATE SET run_id=EXCLUDED.run_id,body=EXCLUDED.body,financial_revision=EXCLUDED.financial_revision,created_at=EXCLUDED.created_at,delivery_state='pending',cancellation_code='' WHERE autonomy_notices.id::text=$8`, memory.ID(), memory.UserID, run, day, at, body, personal.Revision, renewalID)
	return err
}

func digestMoney(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%d,%02d", sign, cents/100, cents%100)
}
