package autonomy

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"openchris/conselho/internal/memory"
	"time"
)

const runColumns = `id::text,status,reason,topic,created_at,completed_at,error_code`
const taskColumns = `id::text,run_id::text,stage,agent_id,status,attempts,error_code,started_at,completed_at,lease_expires_at,result`

func scanRun(row pgx.Row) (r RunSummary, err error) {
	err = row.Scan(&r.ID, &r.Status, &r.Reason, &r.Topic, &r.CreatedAt, &r.CompletedAt, &r.ErrorCode)
	return
}
func scanTask(row pgx.Row) (t TaskSummary, err error) {
	err = row.Scan(&t.ID, &t.RunID, &t.Stage, &t.AgentID, &t.Status, &t.Attempts, &t.ErrorCode, &t.StartedAt, &t.CompletedAt, &t.LeaseExpiresAt, &t.Result)
	return
}
func (s *Service) Status(ctx context.Context) (Status, error) {
	out := Status{Enabled: s.cfg.Enabled, State: "disabled", DailyLimit: s.cfg.DailyLimit, AutoDailyLimit: s.cfg.AutoDailyLimit, Sources: []SourceStatus{}, Runs: []RunSummary{}, Tasks: []TaskSummary{}}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `SELECT paused,heartbeat_at,next_collect_at,next_run_at FROM autonomy_state WHERE user_id=$1`, memory.UserID).Scan(&out.Paused, &out.HeartbeatAt, &out.NextCollectAt, &out.NextRunAt)
	if err != nil && err != pgx.ErrNoRows {
		return out, err
	}
	if s.cfg.Enabled {
		out.State = "starting"
		if out.Paused {
			out.State = "paused"
		} else if out.HeartbeatAt != nil {
			out.State = "active"
			if s.now().Sub(*out.HeartbeatAt) > 3*minute {
				out.State = "stale"
			}
		}
	}
	err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE origin='autonomy') FROM council_attempts WHERE quota_day=($1::timestamptz AT TIME ZONE 'America/Sao_Paulo')::date`, s.now()).Scan(&out.Attempts, &out.AutoAttempts)
	if err != nil {
		return out, err
	}
	var waiting bool
	err = tx.QueryRow(ctx, `SELECT coalesce((SELECT sum(reserved_calls) FROM autonomy_runs WHERE user_id=$1 AND status IN ('pending','running') AND quota_day=($2::timestamptz AT TIME ZONE 'America/Sao_Paulo')::date),0),coalesce((SELECT budget_wait_until>$2 FROM autonomy_state WHERE user_id=$1),false) OR EXISTS(SELECT 1 FROM autonomy_tasks t JOIN autonomy_runs r ON r.id=t.run_id WHERE r.user_id=$1 AND r.status IN ('pending','running') AND t.status='pending' AND t.error_code='waiting_budget')`, memory.UserID, s.now()).Scan(&out.ReservedCalls, &waiting)
	if err != nil {
		return out, err
	}
	if waiting && out.State == "active" {
		out.State = "waiting_budget"
	}
	rows, err := tx.Query(ctx, `SELECT id,name,url,kind,state,error_code,last_checked_at,last_success_at FROM autonomy_sources ORDER BY id`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var source SourceStatus
		err = rows.Scan(&source.ID, &source.Name, &source.URL, &source.Kind, &source.State, &source.ErrorCode, &source.LastCheckedAt, &source.LastSuccessAt)
		if err != nil {
			rows.Close()
			return out, err
		}
		out.Sources = append(out.Sources, source)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = tx.Query(ctx, `SELECT `+runColumns+` FROM (SELECT * FROM autonomy_runs WHERE user_id=$1 ORDER BY (status='running') DESC,(status='pending') DESC,created_at DESC,id DESC LIMIT 20) recent ORDER BY created_at DESC,id DESC`, memory.UserID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		r, e := scanRun(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Runs = append(out.Runs, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = tx.Query(ctx, `SELECT `+taskColumns+` FROM autonomy_tasks WHERE run_id IN (SELECT id FROM autonomy_runs WHERE user_id=$1 ORDER BY (status='running') DESC,(status='pending') DESC,created_at DESC,id DESC LIMIT 10) ORDER BY run_id,stage`, memory.UserID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		t, e := scanTask(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Tasks = append(out.Tasks, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func loadEvidence(ctx context.Context, tx pgx.Tx, id string) ([]Evidence, error) {
	out := []Evidence{}
	rows, err := tx.Query(ctx, `SELECT e.id::text,e.url,e.title,e.summary,e.kind,e.published_at,coalesce(r.observed_at,e.observed_at) FROM autonomy_evidence e JOIN autonomy_run_evidence r ON r.evidence_id=e.id WHERE r.run_id=$1 ORDER BY e.published_at DESC NULLS LAST,coalesce(r.observed_at,e.observed_at) DESC,r.observation_id DESC,e.id`, id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Evidence
		if err = rows.Scan(&e.ID, &e.URL, &e.Title, &e.Summary, &e.Kind, &e.PublishedAt, &e.ObservedAt); err != nil {
			return out, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func loadStrategies(ctx context.Context, tx pgx.Tx) ([]json.RawMessage, error) {
	return loadStrategyArtifacts(ctx, tx, false)
}

// Memory adds the previous independent critique without changing the stored
// strategy.v1 artifact or the authenticated strategy API response.
func loadStrategyMemory(ctx context.Context, tx pgx.Tx) ([]json.RawMessage, error) {
	return loadStrategyArtifacts(ctx, tx, true)
}

func loadStrategyArtifacts(ctx context.Context, tx pgx.Tx, includeCritique bool) ([]json.RawMessage, error) {
	out := []json.RawMessage{}
	rows, err := tx.Query(ctx, `SELECT CASE WHEN $2::boolean THEN v.artifact || jsonb_build_object('prior_critique',v.critique) ELSE v.artifact END FROM autonomy_strategy_versions v JOIN autonomy_strategies s ON s.id=v.strategy_id AND s.current_version=v.version WHERE s.user_id=$1 ORDER BY v.created_at DESC LIMIT 12`, memory.UserID, includeCritique)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw json.RawMessage
		if err = rows.Scan(&raw); err != nil {
			return out, err
		}
		out = append(out, raw)
	}
	return out, rows.Err()
}
func (s *Service) Strategies(ctx context.Context) ([]json.RawMessage, error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	return loadStrategies(ctx, tx)
}
func (s *Service) RunDetail(ctx context.Context, id string) (RunDetail, error) {
	out := RunDetail{Tasks: []TaskSummary{}, Evidence: []Evidence{}, Strategies: []json.RawMessage{}}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	out.Run, err = scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM autonomy_runs WHERE id::text=$1 AND user_id=$2`, id, memory.UserID))
	if err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT `+taskColumns+` FROM autonomy_tasks WHERE run_id=$1 ORDER BY stage`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		t, e := scanTask(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Tasks = append(out.Tasks, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	out.Evidence, err = loadEvidence(ctx, tx, id)
	if err != nil {
		return out, err
	}
	rows, err = tx.Query(ctx, `SELECT artifact FROM autonomy_strategy_versions WHERE run_id=$1 ORDER BY created_at`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var raw json.RawMessage
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return out, err
		}
		out.Strategies = append(out.Strategies, raw)
	}
	err = rows.Err()
	rows.Close()
	return out, err
}
func (s *Service) SetPaused(ctx context.Context, paused bool) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lock(ctx, tx); err != nil {
		return err
	}
	if err = ensure(ctx, tx, s.now()); err != nil {
		return err
	}
	var changed bool
	err = tx.QueryRow(ctx, `WITH previous AS (SELECT paused FROM autonomy_state WHERE user_id=$1),
 updated AS (UPDATE autonomy_state SET paused=$2,notice_epoch=clock_timestamp() WHERE user_id=$1 RETURNING paused)
 SELECT EXISTS(SELECT 1 FROM previous, updated WHERE previous.paused IS DISTINCT FROM updated.paused)`, memory.UserID, paused).Scan(&changed)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE autonomy_notices SET delivery_state='cancelled' WHERE user_id=$1 AND delivery_state='pending'`, memory.UserID)
	}
	if err == nil {
		err = commitActivity(ctx, tx, changed)
	}
	return err
}
func (s *Service) RequestRun(ctx context.Context) (RunSummary, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return RunSummary{}, err
	}
	defer tx.Rollback(ctx)
	if err = lock(ctx, tx); err != nil {
		return RunSummary{}, err
	}
	if err = ensure(ctx, tx, s.now()); err != nil {
		return RunSummary{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE autonomy_state SET requested=true WHERE user_id=$1`, memory.UserID)
	if err != nil {
		return RunSummary{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return RunSummary{}, err
	}
	if err = s.Tick(ctx, s.now()); err != nil {
		return RunSummary{}, err
	}
	var waiting bool
	if err = s.db.QueryRow(ctx, `SELECT coalesce(budget_wait_until>$2,false) AND NOT EXISTS(SELECT 1 FROM autonomy_runs WHERE user_id=$1 AND status IN ('pending','running')) FROM autonomy_state WHERE user_id=$1`, memory.UserID, s.now()).Scan(&waiting); err != nil {
		return RunSummary{}, err
	}
	if waiting {
		return RunSummary{Status: "waiting_budget", Reason: "requested", Topic: "personal_finance"}, nil
	}
	return scanRun(s.db.QueryRow(ctx, `SELECT `+runColumns+` FROM autonomy_runs WHERE user_id=$1 ORDER BY created_at DESC LIMIT 1`, memory.UserID))
}
func (s *Service) PendingNotices(ctx context.Context) ([]Notice, error) {
	out := []Notice{}
	if !s.cfg.Enabled {
		return out, nil
	}
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return out, err
	}
	hour := s.now().In(loc).Hour()
	if hour < 8 || hour >= 22 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `SELECT n.id::text,coalesce(n.run_id::text,''),n.kind FROM autonomy_notices n JOIN autonomy_state s ON s.user_id=n.user_id WHERE n.user_id=$1 AND n.kind='daily' AND NOT s.paused AND n.delivery_state='pending' AND n.created_at>=s.notice_epoch AND (n.created_at AT TIME ZONE 'America/Sao_Paulo')::date=($2::timestamptz AT TIME ZONE 'America/Sao_Paulo')::date ORDER BY n.created_at`, memory.UserID, s.now())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var n Notice
		if err = rows.Scan(&n.ID, &n.RunID, &n.Kind); err != nil {
			return out, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
