package council

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
	"unicode/utf8"

	"openchris/conselho/internal/codex"
	"openchris/conselho/internal/memory"
)

func validateResult(result codex.Result, item job) error {
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > codex.MaxResult || !utf8.ValidString(result.Reply+result.Episode) || strings.ContainsRune(result.Reply+result.Episode, 0) || strings.TrimSpace(result.Reply) == "" || strings.TrimSpace(result.Episode) == "" || result.Model != codex.DefaultModel || result.Evidence == nil || result.Assumptions == nil || result.Unknowns == nil {
		return &codex.Error{Kind: "invalid_output"}
	}
	var data contextData
	if json.Unmarshal(item.Context, &data) != nil {
		return &codex.Error{Kind: "invalid_output"}
	}
	known := map[string]bool{}
	for _, source := range data.Sources {
		known[source.ID] = true
	}
	for _, id := range result.Evidence {
		if !known[id] {
			return &codex.Error{Kind: "invalid_evidence"}
		}
	}
	if result.Usage.InputTokens != nil && *result.Usage.InputTokens < 0 || result.Usage.OutputTokens != nil && *result.Usage.OutputTokens < 0 {
		return &codex.Error{Kind: "invalid_output"}
	}
	return nil
}
func envelope(item job, result codex.Result) map[string]any {
	var data contextData
	json.Unmarshal(item.Context, &data)
	evidence := []map[string]any{}
	for _, id := range result.Evidence {
		for _, source := range data.Sources {
			if source.ID == id {
				evidence = append(evidence, map[string]any{"source_id": id, "source_type": source.Type, "observed_at": source.ObservedAt, "uri": nil, "valid_at": nil, "description": "Fonte do contexto autorizado."})
				break
			}
		}
	}
	return map[string]any{"run_id": item.RunID, "schema_version": "agent.result.v2", "agent_id": item.AgentID, "case_id": item.CaseID, "status": "completed", "artifact_id": item.RunID, "artifact_type": "conversation.analysis.v1", "context_manifest_id": item.ManifestID, "evidence": evidence, "assumptions": result.Assumptions, "unknowns": result.Unknowns, "error_code": nil, "started_at": item.StartedAt, "finished_at": time.Now().UTC(), "model_id": result.Model, "usage": result.Usage, "usage_state": result.Usage.State()}
}
func (s *Service) finish(ctx context.Context, item job, result codex.Result, runErr error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(20261027)`); err != nil {
		return err
	}
	if s.cfg.InvestmentsEnabled {
		// Serialize publication with snapshot/reconnect commits. Row locks also
		// protect consent/error updates which do not take the snapshot lock.
		for _, query := range []string{`SELECT pg_advisory_xact_lock(2026102804)`, `SELECT item_id FROM pluggy_items WHERE user_id=$1 FOR SHARE`, `SELECT external_id FROM pluggy_investments WHERE user_id=$1 FOR SHARE`, `SELECT external_id FROM pluggy_accounts WHERE user_id=$1 FOR SHARE`, `SELECT external_id FROM pluggy_transactions WHERE user_id=$1 FOR SHARE`, `SELECT user_id FROM finance_profile WHERE user_id=$1 FOR SHARE`, `SELECT user_id FROM finance_classifications WHERE user_id=$1 FOR SHARE`, `SELECT user_id FROM finance_classification_rules WHERE user_id=$1 FOR SHARE`} {
			var args []any
			if strings.Contains(query, "$1") {
				args = []any{memory.UserID}
			}
			if _, err = tx.Exec(ctx, query, args...); err != nil {
				return err
			}
		}
	}
	var owns bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM council_jobs WHERE event_id=$1 AND status='running' AND lease_token=$2 AND run_id=$3 AND lease_expires_at>clock_timestamp())`, item.EventID, item.Lease, item.RunID).Scan(&owns)
	if err != nil {
		return err
	}
	if !owns {
		return nil
	}
	code, status := "", "complete"
	if runErr != nil {
		code = codex.Code(runErr)
		status = "failed"
		if code == "context_changed" {
			status = "stale"
		}
		if code == "failed_unknown" || code == "incomplete" {
			status = "failed_unknown"
		}
		var problem *codex.Error
		if errors.As(runErr, &problem) && problem.Transient && code == "busy" && item.Attempts < 2 {
			_, err = tx.Exec(ctx, `UPDATE council_jobs SET status='pending',lease_token=NULL,error_code='busy',next_run_at=clock_timestamp()+interval '2 seconds' WHERE event_id=$1 AND lease_token=$2`, item.EventID, item.Lease)
			if err == nil {
				err = emit(ctx, tx, "agent.failed", map[string]any{"source_event_id": item.EventID, "agent_id": item.AgentID, "state": "failed", "error_code": "busy"})
			}
			if err == nil {
				err = tx.Commit(ctx)
			}
			return err
		}
	} else {
		current, checkErr := fingerprint(ctx, tx, s.cfg.InvestmentsEnabled)
		if checkErr != nil {
			return checkErr
		}
		if current != item.Fingerprint {
			status, code = "stale", "context_changed"
		}
	}
	if status == "complete" {
		raw, _ := json.Marshal(result)
		audit, _ := json.Marshal(envelope(item, result))
		_, err = tx.Exec(ctx, `INSERT INTO council_results(source_event_id,run_id,context_manifest_id,result,envelope) VALUES($1,$2,$3,$4,$5)`, item.EventID, item.RunID, item.ManifestID, raw, audit)
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO conversation_events(user_id,channel,external_key,direction,body,classification) VALUES($1,$2,$3,'outgoing',$4,'restricted')`, memory.UserID, item.Channel, item.Key, result.Reply)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO episodes(user_id,source_event_id,channel,body,outcome) VALUES($1,$2,$3,$4,'analysis_complete') ON CONFLICT(source_event_id) DO UPDATE SET outcome='analysis_complete'`, memory.UserID, item.EventID, item.Channel, item.Text)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE conversation_threads SET last_output_event_id=(SELECT id FROM conversation_events WHERE user_id=$1 AND channel=$2 AND external_key=$3 AND direction='outgoing'),state=CASE WHEN last_input_event_id=$4 THEN 'complete' ELSE 'pending' END,updated_at=clock_timestamp() WHERE user_id=$1 AND channel=$2`, memory.UserID, item.Channel, item.Key, item.EventID)
		}
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE council_jobs SET status=$3,error_code=NULLIF($4,''),lease_token=NULL,completed_at=clock_timestamp() WHERE event_id=$1 AND lease_token=$2`, item.EventID, item.Lease, status, code)
	}
	kind, state := "agent.completed", "idle"
	if status != "complete" {
		kind, state = "agent.failed", "failed"
	}
	if err == nil {
		err = emit(ctx, tx, kind, map[string]any{"source_event_id": item.EventID, "agent_id": item.AgentID, "state": state, "error_code": code})
	}
	if err == nil {
		err = emit(ctx, tx, "analysis."+status, map[string]any{"source_event_id": item.EventID, "channel": item.Channel, "state": status})
	}
	if err == nil && status == "complete" && s.cfg.InvestmentsEnabled {
		// A write can wait until after consent expiry. Recheck after every
		// publication write, then discard all writes if the context expired.
		current, checkErr := fingerprint(ctx, tx, true)
		if checkErr != nil {
			return checkErr
		}
		if current != item.Fingerprint {
			if err = tx.Rollback(ctx); err != nil {
				return err
			}
			return s.finish(ctx, item, codex.Result{}, &codex.Error{Kind: "context_changed"})
		}
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return err
}
func (s *Service) ProcessOne(ctx context.Context) (bool, error) {
	if !s.cfg.Enabled {
		return false, nil
	}
	item, processed, err := s.claim(ctx, s.Status(ctx).State)
	if err != nil || item.EventID == 0 || item.RunID == "" {
		return processed, err
	}
	if s.cfg.InvestmentsEnabled {
		// A busy retry reuses its immutable manifest. Revalidate in a fresh
		// transaction before disclosing that context to the runner again.
		tx, checkErr := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		if checkErr != nil {
			return true, checkErr
		}
		current, checkErr := fingerprint(ctx, tx, true)
		closeErr := tx.Rollback(ctx)
		if checkErr != nil {
			return true, checkErr
		}
		if closeErr != nil {
			return true, closeErr
		}
		if current != item.Fingerprint {
			return true, s.finish(ctx, item, codex.Result{}, &codex.Error{Kind: "context_changed"})
		}
	}
	bounded, cancel := context.WithTimeout(ctx, 185*time.Second)
	result, runErr := s.provider.Run(bounded, codex.Request{RunID: item.RunID, AgentID: item.AgentID, Context: item.Context})
	cancel()
	if runErr == nil {
		runErr = validateResult(result, item)
	}
	if runErr != nil {
		s.rememberFailure(codex.Code(runErr))
	}
	finishCtx, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer done()
	return true, s.finish(finishCtx, item, result, runErr)
}
func (s *Service) Run(ctx context.Context, onError func(error)) {
	if !s.cfg.Enabled {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, err := s.ProcessOne(ctx)
			if err != nil && ctx.Err() == nil && onError != nil {
				onError(err)
			}
		}
	}
}
