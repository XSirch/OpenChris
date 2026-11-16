package autonomy

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"openchris/conselho/internal/codex"
	"openchris/conselho/internal/finance"
	"openchris/conselho/internal/memory"
	"openchris/conselho/internal/research"
)

type task struct {
	ID, RoundID, AgentID, Topic, AttemptID, Lease, Fingerprint string
	Stage, Attempts                                            int
	Context                                                    json.RawMessage
	Cutoff                                                     time.Time
}

func (s *Service) claim(ctx context.Context, state string) (task, bool, error) {
	var item task
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return item, false, err
	}
	defer tx.Rollback(ctx)
	if err = lock(ctx, tx); err != nil {
		return item, false, err
	}
	if err = ensure(ctx, tx, s.now()); err != nil {
		return item, false, err
	}
	// The same lock and counter are also used by conversation workers.
	var blocked bool
	err = tx.QueryRow(ctx, `SELECT paused OR EXISTS(SELECT 1 FROM council_jobs WHERE status IN ('pending','running')) OR EXISTS(SELECT 1 FROM autonomy_tasks WHERE status='running') FROM autonomy_state WHERE user_id=$1`, memory.UserID).Scan(&blocked)
	if err != nil {
		return item, false, err
	}
	if blocked {
		return item, false, tx.Commit(ctx)
	}
	err = tx.QueryRow(ctx, `SELECT t.id::text,t.run_id::text,t.stage,t.agent_id,t.attempts,r.topic,r.fingerprint,r.created_at FROM autonomy_tasks t JOIN autonomy_runs r ON r.id=t.run_id WHERE r.user_id=$1 AND r.status IN ('pending','running') AND t.status='pending' AND t.next_run_at<=$2 AND NOT EXISTS(SELECT 1 FROM autonomy_tasks prior WHERE prior.run_id=t.run_id AND prior.stage<t.stage AND prior.status<>'complete') ORDER BY r.created_at,t.stage LIMIT 1 FOR UPDATE OF t`, memory.UserID, s.now()).Scan(&item.ID, &item.RoundID, &item.Stage, &item.AgentID, &item.Attempts, &item.Topic, &item.Fingerprint, &item.Cutoff)
	if err == pgx.ErrNoRows {
		return task{}, false, tx.Commit(ctx)
	}
	if err != nil {
		return item, false, err
	}
	if state != "available" {
		return task{}, false, waitForActivity(ctx, tx, item.ID, safeState(state))
	}
	var all, auto int
	err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE origin='autonomy') FROM council_attempts WHERE quota_day=($1::timestamptz AT TIME ZONE 'America/Sao_Paulo')::date`, s.now()).Scan(&all, &auto)
	if err != nil {
		return item, false, err
	}
	if all >= s.cfg.DailyLimit || auto >= s.cfg.AutoDailyLimit {
		return task{}, false, waitForActivity(ctx, tx, item.ID, "waiting_budget")
	}
	var reserved, needed int
	var reservedToday bool
	err = tx.QueryRow(ctx, `SELECT reserved_calls,coalesce(quota_day=($2::timestamptz AT TIME ZONE 'America/Sao_Paulo')::date,false),(SELECT coalesce(sum(2-attempts),0) FROM autonomy_tasks WHERE run_id=$1 AND status='pending') FROM autonomy_runs WHERE id=$1`, item.RoundID, s.now()).Scan(&reserved, &reservedToday, &needed)
	if err != nil {
		return item, false, err
	}
	if !reservedToday || reserved < needed {
		if all+needed > s.cfg.DailyLimit || auto+needed > s.cfg.AutoDailyLimit {
			return task{}, false, waitForActivity(ctx, tx, item.ID, "waiting_budget")
		}
		_, err = tx.Exec(ctx, `UPDATE autonomy_runs SET reserved_calls=$2,quota_day=($3::timestamptz AT TIME ZONE 'America/Sao_Paulo')::date WHERE id=$1`, item.RoundID, needed, s.now())
		if err != nil {
			return item, false, err
		}
	}
	fp, err := s.builder.Fingerprint(ctx, tx, s.now())
	if err != nil {
		return item, false, err
	}
	if fp != item.Fingerprint {
		_, err = tx.Exec(ctx, `UPDATE autonomy_runs SET status='stale',error_code='context_changed',completed_at=$2 WHERE id=$1`, item.RoundID, s.now())
		if err == nil {
			err = commitActivity(ctx, tx, true)
		}
		return task{}, true, err
	}
	if changed, checkErr := selectedEvidenceChanged(ctx, tx, item.RoundID); checkErr != nil {
		return item, false, checkErr
	} else if changed {
		_, err = tx.Exec(ctx, `UPDATE autonomy_runs SET status='stale',error_code='evidence_changed',completed_at=$2 WHERE id=$1`, item.RoundID, s.now())
		if err == nil {
			err = commitActivity(ctx, tx, true)
		}
		return task{}, true, err
	}
	if item.Stage > 0 {
		evidence, err := loadEvidence(ctx, tx, item.RoundID)
		if err != nil {
			return item, false, err
		}
		prior := []Report{}
		rows, err := tx.Query(ctx, `SELECT agent_id,stage,result FROM autonomy_tasks WHERE run_id=$1 AND stage>0 AND stage<$2 AND status='complete' AND ($2<>3 OR stage=1) ORDER BY stage`, item.RoundID, item.Stage)
		if err != nil {
			return item, false, err
		}
		for rows.Next() {
			var report Report
			var raw []byte
			if err = rows.Scan(&report.AgentID, &report.Stage, &raw); err != nil {
				rows.Close()
				return item, false, err
			}
			if err = json.Unmarshal(raw, &report.Result); err != nil {
				rows.Close()
				return item, false, err
			}
			prior = append(prior, report)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return item, false, err
		}
		strategies, err := loadStrategyMemory(ctx, tx)
		if err != nil {
			return item, false, err
		}
		snapshot, err := s.builder.Build(ctx, tx, ContextInput{TaskID: item.ID, RoundID: item.RoundID, AgentID: item.AgentID, Stage: item.Stage, Topic: item.Topic, Evidence: evidence, Prior: prior, Strategies: strategies})
		if err != nil {
			return item, false, err
		}
		if snapshot.Fingerprint != item.Fingerprint {
			return item, false, &codex.Error{Kind: "context_changed"}
		}
		item.Context = snapshot.Context
		if err = (codex.Request{RunID: memory.ID(), AgentID: item.AgentID, Context: item.Context}).Validate(); err != nil {
			return item, false, err
		}
	}
	item.AttemptID, item.Lease = memory.ID(), memory.ID()
	item.Attempts++
	_, err = tx.Exec(ctx, `UPDATE autonomy_tasks SET status='running',attempts=$2,attempt_id=$3,lease_token=$4,lease_expires_at=$5::timestamptz+interval '240 seconds',started_at=$5,error_code='',context=$6,fingerprint=$7 WHERE id=$1`, item.ID, item.Attempts, item.AttemptID, item.Lease, s.now(), nullableJSON(item.Context), item.Fingerprint)
	if err != nil {
		return item, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO council_attempts(run_id,origin,task_id,quota_day,started_at) VALUES($1,'autonomy',$2,($3::timestamptz AT TIME ZONE 'America/Sao_Paulo')::date,$3)`, item.AttemptID, item.ID, s.now())
	if err != nil {
		return item, false, err
	}
	_, err = tx.Exec(ctx, `UPDATE autonomy_runs SET status='running',reserved_calls=greatest(reserved_calls-1,0) WHERE id=$1`, item.RoundID)
	if err != nil {
		return item, false, err
	}
	return item, true, commitActivity(ctx, tx, true)
}
func nullableJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}
func safeState(state string) string {
	switch state {
	case "busy", "not_authenticated", "rate_limited", "unavailable", "disabled":
		return state
	default:
		return "unavailable"
	}
}
func (s *Service) preflight(ctx context.Context, item task) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lock(ctx, tx); err != nil {
		return err
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM autonomy_tasks t JOIN autonomy_state s ON s.user_id=$4 WHERE t.id=$1 AND t.lease_token=$2 AND t.status='running' AND t.lease_expires_at>$3 AND NOT s.paused)`, item.ID, item.Lease, s.now(), memory.UserID).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return &codex.Error{Kind: "paused"}
	}
	fp, err := s.builder.Fingerprint(ctx, tx, s.now())
	if err != nil {
		return err
	}
	if fp != item.Fingerprint {
		return &codex.Error{Kind: "context_changed"}
	}
	if changed, err := selectedEvidenceChanged(ctx, tx, item.RoundID); err != nil {
		return err
	} else if changed {
		return &codex.Error{Kind: "context_changed"}
	}
	return nil
}
func (s *Service) ProcessOne(ctx context.Context) (bool, error) {
	if !s.cfg.Enabled {
		return false, nil
	}
	status, err := s.provider.Status(ctx)
	if err != nil {
		status.State = "unavailable"
	}
	item, processed, err := s.claim(ctx, status.State)
	if err != nil || item.ID == "" {
		return processed, err
	}
	runErr := s.preflight(ctx, item)
	var result codex.Result
	var public codex.ResearchResult
	docs := []research.Document{}
	if runErr == nil {
		timeout := 185 * time.Second
		if item.Stage == 0 {
			timeout = 120 * time.Second
		}
		bounded, cancel := context.WithTimeout(ctx, timeout)
		if item.Stage == 0 {
			topic := item.Topic
			if topic == "personal_finance" {
				topic = "fixed_income"
			}
			public, runErr = s.provider.Research(bounded, codex.ResearchRequest{RunID: item.AttemptID, Topic: topic, AsOf: item.Cutoff})
			if runErr == nil {
				if public.Model != codex.DefaultModel || strings.TrimSpace(public.Summary) == "" || len(public.Sources) > 12 {
					runErr = &codex.Error{Kind: "invalid_output"}
				}
			}
			if runErr == nil {
				verified := []codex.ResearchSource{}
				for _, source := range public.Sources {
					doc, verifyErr := s.collector.VerifyDocument(bounded, source.URL)
					if verifyErr != nil {
						public.Unknowns = append(public.Unknowns, "Referência pública indisponível para verificação.")
						continue
					}
					if doc.PublishedAt != nil && doc.PublishedAt.After(item.Cutoff) {
						public.Unknowns = append(public.Unknowns, "Referência pública publicada após o corte temporal da rodada.")
						continue
					}
					if doc.PublishedAt == nil {
						public.Unknowns = append(public.Unknowns, "Data de publicação da referência pública não confirmada.")
					}
					docs = append(docs, doc)
					verified = append(verified, codex.ResearchSource{URL: doc.URL, Title: doc.Title, Summary: doc.Summary, PublishedAt: doc.PublishedAt})
				}
				public.Sources = verified
			}
		} else {
			result, runErr = s.provider.Run(bounded, codex.Request{RunID: item.AttemptID, AgentID: item.AgentID, Context: item.Context})
			if runErr == nil {
				runErr = validateResult(result, item.Context)
				if item.Stage == 1 && result.Strategy == nil {
					runErr = &codex.Error{Kind: "invalid_output"}
				}
			}
		}
		cancel()
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	return true, s.finish(finishCtx, item, result, public, docs, runErr)
}
func validateResult(result codex.Result, raw json.RawMessage) error {
	bytes, err := json.Marshal(result)
	if err != nil || len(bytes) > codex.MaxResult || !utf8.Valid(bytes) || strings.TrimSpace(result.Reply) == "" || strings.TrimSpace(result.Episode) == "" || strings.ContainsRune(result.Reply+result.Episode, 0) || result.Model != codex.DefaultModel || result.Evidence == nil || result.Assumptions == nil || result.Unknowns == nil {
		return &codex.Error{Kind: "invalid_output"}
	}
	var c struct {
		Sources []struct {
			ID string `json:"id"`
		} `json:"sources"`
	}
	if json.Unmarshal(raw, &c) != nil {
		return &codex.Error{Kind: "invalid_output"}
	}
	known := map[string]bool{}
	for _, source := range c.Sources {
		known[source.ID] = true
	}
	refs := append([]string{}, result.Evidence...)
	if result.Strategy != nil {
		refs = append(refs, result.Strategy.Evidence...)
	}
	for _, id := range refs {
		if !known[id] {
			return &codex.Error{Kind: "invalid_evidence"}
		}
	}
	if result.Usage.InputTokens != nil && *result.Usage.InputTokens < 0 || result.Usage.OutputTokens != nil && *result.Usage.OutputTokens < 0 {
		return &codex.Error{Kind: "invalid_output"}
	}
	return nil
}
func (s *Service) finish(ctx context.Context, item task, result codex.Result, public codex.ResearchResult, docs []research.Document, runErr error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lock(ctx, tx); err != nil {
		return err
	}
	for _, query := range []string{`SELECT pg_advisory_xact_lock(2026102804)`, `SELECT item_id FROM pluggy_items WHERE user_id=$1 FOR SHARE`, `SELECT external_id FROM pluggy_investments WHERE user_id=$1 FOR SHARE`, `SELECT external_id FROM pluggy_accounts WHERE user_id=$1 FOR SHARE`, `SELECT external_id FROM pluggy_transactions WHERE user_id=$1 FOR SHARE`, `SELECT user_id FROM finance_profile WHERE user_id=$1 FOR SHARE`, `SELECT user_id FROM finance_classifications WHERE user_id=$1 FOR SHARE`, `SELECT user_id FROM finance_classification_rules WHERE user_id=$1 FOR SHARE`} {
		var args []any
		if strings.Contains(query, "$1") {
			args = []any{memory.UserID}
		}
		if _, err = tx.Exec(ctx, query, args...); err != nil {
			return err
		}
	}
	var owns, paused bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM autonomy_tasks WHERE id=$1 AND status='running' AND lease_token=$2 AND lease_expires_at>$3),paused FROM autonomy_state WHERE user_id=$4 FOR UPDATE`, item.ID, item.Lease, s.now(), memory.UserID).Scan(&owns, &paused)
	if err != nil {
		return err
	}
	if !owns {
		return nil
	}
	if runErr == nil && paused {
		runErr = &codex.Error{Kind: "paused"}
	}
	if runErr == nil {
		fp, checkErr := s.builder.Fingerprint(ctx, tx, s.now())
		if checkErr != nil {
			runErr = checkErr
		} else if fp != item.Fingerprint {
			runErr = &codex.Error{Kind: "context_changed"}
		}
	}
	if runErr == nil {
		if changed, checkErr := selectedEvidenceChanged(ctx, tx, item.RoundID); checkErr != nil {
			runErr = checkErr
		} else if changed {
			runErr = &codex.Error{Kind: "context_changed"}
		}
	}
	if runErr != nil {
		return s.failTx(ctx, tx, item, runErr)
	}
	if item.Stage == 0 {
		for _, doc := range docs {
			u, _ := url.Parse(doc.URL)
			if u == nil || u.Hostname() == "" {
				return s.failTx(ctx, tx, item, &codex.Error{Kind: "invalid_evidence"})
			}
			source := "web-" + digest(u.Hostname())[:20]
			_, err = tx.Exec(ctx, `INSERT INTO autonomy_sources(id,name,url,kind,is_feed,state,last_checked_at,last_success_at) VALUES($1,$2,$3,$4,false,'ok',$5,$5) ON CONFLICT(id) DO UPDATE SET state='ok',error_code='',last_checked_at=EXCLUDED.last_checked_at,last_success_at=EXCLUDED.last_success_at`, source, u.Hostname(), "https://"+u.Hostname()+"/", doc.Kind, s.now())
			if err != nil {
				return err
			}
			observedAt := s.now()
			if !doc.CollectedAt.IsZero() {
				observedAt = doc.CollectedAt
			}
			id, err := saveDocument(ctx, tx, source, doc, observedAt)
			if err != nil {
				return err
			}
			// Research may replace an RSS extract with the verified page for the
			// same URL before any private stage consumes the dossier. Keep both
			// immutable evidence versions, selecting only the verified document.
			// The pre-write check above still rejects external corrections made
			// while the public inference was in flight.
			_, err = tx.Exec(ctx, `DELETE FROM autonomy_run_evidence r USING autonomy_evidence e WHERE r.run_id=$1 AND r.evidence_id=e.id AND e.url=$2 AND e.id<>$3`, item.RoundID, doc.URL, id)
			if err != nil {
				return err
			}
			var observationID int64
			if err = tx.QueryRow(ctx, `SELECT id FROM autonomy_evidence_observations WHERE source_id=$1 AND evidence_id=$2 AND observed_at=$3 ORDER BY id DESC LIMIT 1`, source, id, observedAt).Scan(&observationID); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `INSERT INTO autonomy_run_evidence(run_id,evidence_id,observed_at,observation_id) VALUES($1,$2,$3,$4) ON CONFLICT(run_id,evidence_id) DO UPDATE SET observed_at=EXCLUDED.observed_at,observation_id=EXCLUDED.observation_id`, item.RoundID, id, observedAt, observationID)
			if err != nil {
				return err
			}
		}
	}
	var raw []byte
	if item.Stage == 0 {
		raw, err = json.Marshal(public)
	} else {
		raw, err = json.Marshal(result)
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE autonomy_tasks SET status='complete',result=$3,lease_token=NULL,completed_at=$4 WHERE id=$1 AND lease_token=$2`, item.ID, item.Lease, raw, s.now())
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE autonomy_runs SET reserved_calls=greatest(reserved_calls-$2,0) WHERE id=$1`, item.RoundID, 2-item.Attempts); err != nil {
		return err
	}
	if item.Stage == 4 {
		var count int
		err = tx.QueryRow(ctx, `SELECT count(*) FROM autonomy_tasks WHERE run_id=$1 AND status='complete'`, item.RoundID).Scan(&count)
		if err != nil {
			return err
		}
		if count != 5 {
			return &codex.Error{Kind: "incomplete"}
		}
		if err = s.persistStrategy(ctx, tx, item); err != nil {
			return err
		}
		personal, profileErr := finance.ReadPersonalFinance(ctx, tx, memory.UserID, s.now())
		if profileErr != nil {
			return profileErr
		}
		status, completionCode := "complete", ""
		var snapshot struct {
			Personal *finance.PersonalFinance `json:"personal_finance"`
		}
		if json.Unmarshal(item.Context, &snapshot) != nil || snapshot.Personal == nil || !snapshot.Personal.CanRecommend || !snapshot.Personal.ProfileComplete || !personal.CanRecommend {
			status, completionCode = "await_data", "personal_data_missing"
		}
		var healthy bool
		var usable int
		err = tx.QueryRow(ctx, `SELECT sources_healthy FROM autonomy_runs WHERE id=$1`, item.RoundID).Scan(&healthy)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `SELECT count(*) FROM autonomy_run_evidence r JOIN autonomy_evidence e ON e.id=r.evidence_id WHERE r.run_id=$1 AND e.published_at IS NOT NULL AND e.published_at<=$2 AND (e.kind='official_law' OR e.published_at>=$2::timestamptz-interval '7 days')`, item.RoundID, item.Cutoff).Scan(&usable)
		if err != nil {
			return err
		}
		var gaps bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM autonomy_tasks WHERE run_id=$1 AND ((result->'unknowns') IS NULL OR jsonb_array_length(result->'unknowns')>0))`, item.RoundID).Scan(&gaps)
		if err != nil {
			return err
		}
		if !healthy || usable == 0 || gaps {
			status = "await_data"
			if !healthy {
				completionCode = "source_unavailable"
			} else if usable == 0 {
				completionCode = "dated_evidence_missing"
			} else if gaps {
				completionCode = "required_data_missing"
			}
		}
		if status == "complete" && item.Topic == "personal_finance" {
			// A current public page must actually support both the strategy
			// and the synthesis. Merely collecting a feed or citing only the
			// financial snapshot is insufficient for a product recommendation.
			var supported int
			err = tx.QueryRow(ctx, `SELECT count(DISTINCT t.stage) FROM autonomy_tasks t WHERE t.run_id=$1 AND t.stage IN (1,4) AND EXISTS (
 SELECT 1 FROM autonomy_run_evidence r JOIN autonomy_evidence e ON e.id=r.evidence_id JOIN autonomy_evidence_observations o ON o.id=r.observation_id JOIN autonomy_sources s ON s.id=o.source_id
 WHERE r.run_id=t.run_id AND NOT s.is_feed AND e.published_at<=$2 AND e.published_at>=$2::timestamptz-interval '7 days'
 AND CASE WHEN t.stage=1 THEN t.result->'strategy'->'evidence' ELSE t.result->'evidence' END ? e.id::text)`, item.RoundID, item.Cutoff).Scan(&supported)
			if err != nil {
				return err
			}
			if supported != 2 {
				status, completionCode = "await_data", "product_evidence_missing"
			}
		}
		_, err = tx.Exec(ctx, `UPDATE autonomy_runs SET status=$2,completed_at=$3,error_code=$4 WHERE id=$1`, item.RoundID, status, s.now(), completionCode)
		if err != nil {
			return err
		}
		if err = s.createNotices(ctx, tx, s.now()); err != nil {
			return err
		}
	}
	// All publication writes may have waited on locks. Validate again at the
	// actual end of the transaction and discard every artifact if it expired.
	fp, checkErr := s.builder.Fingerprint(ctx, tx, s.now())
	changed, evidenceErr := selectedEvidenceChanged(ctx, tx, item.RoundID)
	if checkErr != nil || fp != item.Fingerprint || changed || evidenceErr != nil {
		tx.Rollback(ctx)
		return s.finish(ctx, item, codex.Result{}, codex.ResearchResult{}, nil, &codex.Error{Kind: "context_changed"})
	}
	err = tx.QueryRow(ctx, `SELECT paused FROM autonomy_state WHERE user_id=$1`, memory.UserID).Scan(&paused)
	if err != nil {
		return err
	}
	if paused {
		tx.Rollback(ctx)
		return s.finish(ctx, item, codex.Result{}, codex.ResearchResult{}, nil, &codex.Error{Kind: "paused"})
	}
	return commitActivity(ctx, tx, true)
}

func selectedEvidenceChanged(ctx context.Context, tx pgx.Tx, runID string) (bool, error) {
	var changed bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM autonomy_run_evidence r JOIN autonomy_evidence e ON e.id=r.evidence_id JOIN autonomy_evidence_observations newer ON newer.url=e.url AND newer.evidence_id<>e.id AND (newer.observed_at>coalesce(r.observed_at,e.observed_at) OR (newer.observed_at=coalesce(r.observed_at,e.observed_at) AND newer.id>coalesce(r.observation_id,0))) WHERE r.run_id=$1)`, runID).Scan(&changed)
	return changed, err
}
func (s *Service) failTx(ctx context.Context, tx pgx.Tx, item task, problem error) error {
	code := codex.Code(problem)
	status := "failed"
	round := "partial"
	if code == "failed_unknown" || code == "incomplete" {
		status = "failed_unknown"
	}
	if code == "context_changed" {
		status, round = "stale", "stale"
	}
	if code == "paused" {
		status = "cancelled"
	}
	var known *codex.Error
	if errors.As(problem, &known) && known.Transient && code == "busy" && item.Attempts < 2 {
		_, err := tx.Exec(ctx, `UPDATE autonomy_tasks SET status='pending',lease_token=NULL,error_code='busy',next_run_at=$2::timestamptz+interval '2 seconds' WHERE id=$1`, item.ID, s.now())
		if err != nil {
			return err
		}
		return commitActivity(ctx, tx, true)
	}
	_, err := tx.Exec(ctx, `UPDATE autonomy_tasks SET status=$2,error_code=$3,lease_token=NULL,completed_at=$4 WHERE id=$1`, item.ID, status, code, s.now())
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE autonomy_runs SET status=$2,error_code=$3,completed_at=$4 WHERE id=$1`, item.RoundID, round, code, s.now())
	if err != nil {
		return err
	}
	if err = s.createNotices(ctx, tx, s.now()); err != nil {
		return err
	}
	return commitActivity(ctx, tx, true)
}

func commitActivity(ctx context.Context, tx pgx.Tx, changed bool) error {
	if changed {
		if _, err := tx.Exec(ctx, `INSERT INTO stream_events(user_id,event_type,payload) VALUES($1,'autonomy.changed','{}')`, memory.UserID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func waitForActivity(ctx context.Context, tx pgx.Tx, id, code string) error {
	changed, err := tx.Exec(ctx, `UPDATE autonomy_tasks SET error_code=$2 WHERE id=$1 AND error_code IS DISTINCT FROM $2`, id, code)
	if err != nil {
		return err
	}
	return commitActivity(ctx, tx, changed.RowsAffected() > 0)
}
