package council

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"openchris/conselho/internal/codex"
	"openchris/conselho/internal/memory"
)

type job struct {
	EventID                                                                    int64
	CaseID, AgentID, Text, Channel, Key, RunID, Lease, ManifestID, Fingerprint string
	Attempts                                                                   int
	Context                                                                    json.RawMessage
	StartedAt                                                                  time.Time
}

func emit(ctx context.Context, tx pgx.Tx, kind string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO stream_events(user_id,event_type,payload) VALUES($1,$2,$3)`, memory.UserID, kind, raw)
	return err
}
func (s *Service) claim(ctx context.Context, state string) (job, bool, error) {
	var item job
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return item, false, err
	}
	locked := false
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		if !locked || conn.QueryRow(cleanup, `SELECT pg_advisory_unlock(20261027)`).Scan(&unlocked) != nil || !unlocked {
			conn.Hijack().Close(cleanup)
			return
		}
		conn.Release()
	}()
	// Establish the repeatable snapshot only after prior admissions commit.
	// A transaction-level lock as the first RR query takes its snapshot too early.
	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock(20261027)`); err != nil {
		return item, false, err
	}
	locked = true
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return item, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(20261027)`); err != nil {
		return item, false, err
	}
	// Recovery remains available when the automatic supervisor is disabled.
	// An abandoned operation becomes unknown and is never replayed here.
	automatic, err := tx.Exec(ctx, `WITH expired AS (UPDATE autonomy_tasks SET status='failed_unknown',error_code='failed_unknown',lease_token=NULL,completed_at=clock_timestamp() WHERE status='running' AND lease_expires_at<=clock_timestamp() RETURNING run_id) UPDATE autonomy_runs SET status='partial',error_code='failed_unknown',completed_at=clock_timestamp() WHERE id IN(SELECT run_id FROM expired)`)
	if err != nil {
		return item, false, err
	}
	if automatic.RowsAffected() > 0 {
		if err = emit(ctx, tx, "autonomy.changed", struct{}{}); err != nil {
			return item, false, err
		}
	}
	rows, err := tx.Query(ctx, `UPDATE council_jobs SET status='failed_unknown',error_code='failed_unknown',lease_token=NULL,completed_at=clock_timestamp() WHERE status='running' AND lease_expires_at<=clock_timestamp() RETURNING event_id,agent_id`)
	if err != nil {
		return item, false, err
	}
	recovered := []job{}
	for rows.Next() {
		var expired job
		if err = rows.Scan(&expired.EventID, &expired.AgentID); err != nil {
			rows.Close()
			return item, false, err
		}
		recovered = append(recovered, expired)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return item, false, err
	}
	for _, expired := range recovered {
		if err = emit(ctx, tx, "agent.failed", map[string]any{"source_event_id": expired.EventID, "agent_id": expired.AgentID, "state": "failed", "error_code": "failed_unknown"}); err != nil {
			return item, false, err
		}
	}
	var running bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM council_jobs WHERE status='running') OR EXISTS(SELECT 1 FROM autonomy_tasks WHERE status='running')`).Scan(&running); err != nil {
		return item, false, err
	}
	if running || state == "busy" {
		return item, len(recovered) > 0, tx.Commit(ctx)
	}
	var ready bool
	var manifest *string
	err = tx.QueryRow(ctx, `SELECT j.event_id,j.case_id::text,j.agent_id,j.attempts,j.context_manifest_id::text,e.body,e.channel,e.external_key,j.next_run_at<=clock_timestamp() FROM council_jobs j JOIN conversation_events e ON e.id=j.event_id WHERE j.status='pending' ORDER BY j.event_id FOR UPDATE OF j SKIP LOCKED LIMIT 1`).Scan(&item.EventID, &item.CaseID, &item.AgentID, &item.Attempts, &manifest, &item.Text, &item.Channel, &item.Key, &ready)
	if errors.Is(err, pgx.ErrNoRows) {
		return job{}, len(recovered) > 0, tx.Commit(ctx)
	}
	if err != nil {
		return item, false, err
	}
	if !ready {
		return job{}, len(recovered) > 0, tx.Commit(ctx)
	}
	if state != "available" {
		_, err = tx.Exec(ctx, `UPDATE council_jobs SET status='blocked',error_code=$2,completed_at=clock_timestamp() WHERE event_id=$1`, item.EventID, state)
		if err == nil {
			err = emit(ctx, tx, "analysis.failed", map[string]any{"source_event_id": item.EventID, "state": "blocked", "error_code": state})
		}
		if err == nil {
			err = tx.Commit(ctx)
		}
		return job{}, true, err
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM council_attempts WHERE quota_day=(clock_timestamp() AT TIME ZONE 'America/Sao_Paulo')::date)+coalesce((SELECT sum(reserved_calls) FROM autonomy_runs WHERE status IN ('pending','running') AND quota_day=(clock_timestamp() AT TIME ZONE 'America/Sao_Paulo')::date),0)`).Scan(&count); err != nil {
		return item, false, err
	}
	if count >= s.cfg.DailyLimit {
		_, err = tx.Exec(ctx, `UPDATE council_jobs SET status='blocked',error_code='rate_limited',completed_at=clock_timestamp() WHERE event_id=$1`, item.EventID)
		if err == nil {
			err = tx.Commit(ctx)
		}
		return job{}, true, err
	}
	if manifest == nil {
		if err = explicitPreference(ctx, tx, item); err != nil {
			return item, false, err
		}
		data, hash, buildErr := buildContext(ctx, tx, item, s.cfg.InvestmentsEnabled)
		if buildErr != nil {
			if codex.Code(buildErr) != "privacy_blocked" {
				return item, false, buildErr
			}
			_, err = tx.Exec(ctx, `UPDATE council_jobs SET status='blocked',error_code='privacy_blocked',completed_at=clock_timestamp() WHERE event_id=$1`, item.EventID)
			if err == nil {
				err = tx.Commit(ctx)
			}
			return job{}, true, err
		}
		item.ManifestID = memory.ID()
		item.Fingerprint = hash
		item.Context, err = json.Marshal(data)
		if err != nil {
			return item, false, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO council_manifests(id,source_event_id,snapshot,fingerprint) VALUES($1,$2,$3,$4)`, item.ManifestID, item.EventID, item.Context, item.Fingerprint)
		if err != nil {
			return item, false, err
		}
	} else {
		item.ManifestID = *manifest
		if err = tx.QueryRow(ctx, `SELECT snapshot,fingerprint FROM council_manifests WHERE id=$1`, item.ManifestID).Scan(&item.Context, &item.Fingerprint); err != nil {
			return item, false, err
		}
	}
	item.RunID = memory.ID()
	item.Lease = memory.ID()
	item.Attempts++
	item.StartedAt = time.Now().UTC()
	_, err = tx.Exec(ctx, `UPDATE council_jobs SET status='running',attempts=$2,context_manifest_id=$3,run_id=$4,lease_token=$5,lease_expires_at=clock_timestamp()+interval '240 seconds',error_code=NULL WHERE event_id=$1`, item.EventID, item.Attempts, item.ManifestID, item.RunID, item.Lease)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO council_attempts(run_id,event_id,quota_day) VALUES($1,$2,(clock_timestamp() AT TIME ZONE 'America/Sao_Paulo')::date)`, item.RunID, item.EventID)
	}
	if err == nil {
		err = emit(ctx, tx, "agent.started", map[string]any{"source_event_id": item.EventID, "agent_id": item.AgentID, "state": "running", "task_title_safe": "Analisar conversa"})
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return item, true, err
}
