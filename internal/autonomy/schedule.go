package autonomy

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"openchris/conselho/internal/memory"
	"openchris/conselho/internal/research"
)

func (s *Service) Tick(ctx context.Context, now time.Time) error {
	if !s.cfg.Enabled {
		return nil
	}
	now = now.UTC()
	token, err := s.heartbeat(ctx, now)
	if err != nil {
		return err
	}
	if token != "" {
		if err = s.collect(ctx, now, token); err != nil {
			return err
		}
		if finished := s.now(); finished.After(now) {
			now = finished
		}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lock(ctx, tx); err != nil {
		return err
	}
	var paused, requested, collecting bool
	var next time.Time
	var last *time.Time
	var previous string
	err = tx.QueryRow(ctx, `SELECT paused,requested,next_run_at,last_run_at,material_fingerprint,collect_token IS NOT NULL FROM autonomy_state WHERE user_id=$1 FOR UPDATE`, memory.UserID).Scan(&paused, &requested, &next, &last, &previous, &collecting)
	if err != nil {
		return err
	}
	if paused || collecting {
		return tx.Commit(ctx)
	}
	var open bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM autonomy_runs WHERE user_id=$1 AND status IN ('pending','running'))`, memory.UserID).Scan(&open); err != nil {
		return err
	}
	if open {
		return tx.Commit(ctx)
	}
	fp, err := s.builder.Fingerprint(ctx, tx, now)
	if err != nil {
		return err
	}
	ef, err := evidenceFingerprint(ctx, tx, now)
	if err != nil {
		return err
	}
	// Public research is an input to a round, never a financial change.
	material := fp
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return err
	}
	local := now.In(loc)
	morning := time.Date(local.Year(), local.Month(), local.Day(), 7, 0, 0, 0, loc)
	var personalToday bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM autonomy_runs WHERE user_id=$1 AND topic='personal_finance' AND created_at>=$2 AND created_at<$3)`, memory.UserID, morning, morning.AddDate(0, 0, 1)).Scan(&personalToday); err != nil {
		return err
	}
	reason := "scheduled"
	due := !now.Before(next)
	if last == nil {
		due = true
		reason = "initial"
	} else if requested {
		due = true
		reason = "requested"
	} else if material != previous && now.Sub(*last) >= time.Hour {
		due = true
		reason = "material_change"
	} else if !now.Before(morning) && !personalToday {
		due = true
		reason = "personal_daily"
	}
	if !due {
		return tx.Commit(ctx)
	}
	id := memory.ID()
	topic := topics[0]
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM autonomy_runs WHERE user_id=$1`, memory.UserID).Scan(&count); err != nil {
		return err
	}
	topic = topics[count%len(topics)]
	if reason != "scheduled" {
		topic = "personal_finance"
	}
	var all, auto int
	if err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE origin='autonomy') FROM council_attempts WHERE quota_day=($1::timestamptz AT TIME ZONE 'America/Sao_Paulo')::date`, now).Scan(&all, &auto); err != nil {
		return err
	}
	needed := 10
	if topic != "personal_finance" && !personalToday {
		needed += 10
	}
	if all+needed > s.cfg.DailyLimit || auto+needed > s.cfg.AutoDailyLimit {
		changed, err := tx.Exec(ctx, `UPDATE autonomy_state SET budget_wait_until=$2,next_run_at=$2 WHERE user_id=$1 AND budget_wait_until IS DISTINCT FROM $2`, memory.UserID, morning.AddDate(0, 0, 1))
		if err != nil {
			return err
		}
		return commitActivity(ctx, tx, changed.RowsAffected() > 0)
	}
	var healthy bool
	err = tx.QueryRow(ctx, `SELECT count(*)>0 AND bool_and(state IN ('ok','unchanged') AND last_success_at>=$1::timestamptz-interval '30 minutes') FROM autonomy_sources WHERE enabled AND is_feed`, now).Scan(&healthy)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO autonomy_runs(id,user_id,reason,topic,fingerprint,evidence_fingerprint,sources_healthy,created_at,reserved_calls,quota_day) VALUES($1,$2,$3,$4,$5,$6,$7,$8,10,($8::timestamptz AT TIME ZONE 'America/Sao_Paulo')::date)`, id, memory.UserID, reason, topic, fp, ef, healthy, now)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO autonomy_run_evidence(run_id,evidence_id,observed_at,observation_id) SELECT $1,evidence_id,observed_at,observation_id FROM (SELECT DISTINCT ON(e.url) e.id AS evidence_id,e.url,e.published_at,o.observed_at,o.id AS observation_id FROM autonomy_evidence_observations o JOIN autonomy_evidence e ON e.id=o.evidence_id WHERE o.observed_at<=$2 AND (e.published_at IS NULL OR e.published_at<=$2) ORDER BY e.url,o.observed_at DESC,o.id DESC) latest ORDER BY published_at DESC NULLS LAST,observed_at DESC,observation_id DESC LIMIT 12`, id, now)
	if err != nil {
		return err
	}
	agents := []string{researchAgent(topic), strategyAgent(topic), "A17", "A21", "A01"}
	for stage, agent := range agents {
		_, err = tx.Exec(ctx, `INSERT INTO autonomy_tasks(id,run_id,stage,agent_id,next_run_at) VALUES($1,$2,$3,$4,$5)`, memory.ID(), id, stage, agent, now)
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE autonomy_state SET last_run_at=$2,next_run_at=$2::timestamptz+interval '6 hours',material_fingerprint=$3,requested=false,budget_wait_until=NULL WHERE user_id=$1`, memory.UserID, now, material)
	if err != nil {
		return err
	}
	if err = s.createNotices(ctx, tx, now); err != nil {
		return err
	}
	return commitActivity(ctx, tx, true)
}

var topics = []string{"brazil_macro", "fixed_income", "funds", "long_term", "global", "crypto"}

func researchAgent(topic string) string {
	switch topic {
	case "personal_finance":
		return "A13"
	case "fixed_income":
		return "A13"
	case "funds":
		return "A18"
	case "global":
		return "A15"
	case "crypto":
		return "A16"
	default:
		return "A14"
	}
}
func strategyAgent(topic string) string {
	switch topic {
	case "personal_finance":
		return "A08"
	case "long_term":
		return "A11"
	case "crypto":
		return "A12"
	default:
		return "A10"
	}
}
func evidenceFingerprint(ctx context.Context, tx pgx.Tx, at time.Time) (string, error) {
	var ids string
	err := tx.QueryRow(ctx, `SELECT coalesce(string_agg(id::text,',' ORDER BY id),'') FROM (SELECT DISTINCT ON(e.url) e.id,e.url FROM autonomy_evidence_observations o JOIN autonomy_evidence e ON e.id=o.evidence_id WHERE o.observed_at<=$1 AND (e.published_at IS NULL OR e.published_at<=$1) ORDER BY e.url,o.observed_at DESC,o.id DESC) e`, at).Scan(&ids)
	return digest(ids), err
}
func (s *Service) heartbeat(ctx context.Context, now time.Time) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if err = lock(ctx, tx); err != nil {
		return "", err
	}
	if err = ensure(ctx, tx, now); err != nil {
		return "", err
	}
	for _, source := range research.DefaultFeeds() {
		_, err = tx.Exec(ctx, `INSERT INTO autonomy_sources(id,name,url,kind) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO NOTHING`, source.ID, source.Name, source.URL, source.Kind)
		if err != nil {
			return "", err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE autonomy_state SET heartbeat_at=$2 WHERE user_id=$1`, memory.UserID, now)
	if err != nil {
		return "", err
	}
	// Recovery never repeats an external operation whose result is unknown.
	recovered, err := tx.Exec(ctx, `WITH expired AS (UPDATE autonomy_tasks SET status='failed_unknown',error_code='failed_unknown',lease_token=NULL,completed_at=$1 WHERE status='running' AND lease_expires_at<=$1 RETURNING run_id) UPDATE autonomy_runs SET status='partial',error_code='failed_unknown',completed_at=$1 WHERE id IN(SELECT run_id FROM expired)`, now)
	if err != nil {
		return "", err
	}
	var token string
	err = tx.QueryRow(ctx, `UPDATE autonomy_state SET collect_token=$2,collect_expires_at=$3::timestamptz+interval '3 minutes',next_collect_at=$3::timestamptz+interval '15 minutes' WHERE user_id=$1 AND NOT paused AND next_collect_at<=$3 AND (collect_token IS NULL OR collect_expires_at<=$3) RETURNING collect_token::text`, memory.UserID, memory.ID(), now).Scan(&token)
	if err != nil && err != pgx.ErrNoRows {
		return "", err
	}
	if err = s.createNotices(ctx, tx, now); err != nil {
		return "", err
	}
	return token, commitActivity(ctx, tx, recovered.RowsAffected() > 0)
}
func (s *Service) collect(ctx context.Context, now time.Time, token string) error {
	rows, err := s.db.Query(ctx, `SELECT id,name,url,kind,etag,last_modified FROM autonomy_sources WHERE enabled AND is_feed ORDER BY id`)
	if err != nil {
		return err
	}
	type feed struct {
		source         research.Source
		etag, modified string
	}
	feeds := []feed{}
	for rows.Next() {
		var f feed
		if err = rows.Scan(&f.source.ID, &f.source.Name, &f.source.URL, &f.source.Kind, &f.etag, &f.modified); err != nil {
			rows.Close()
			return err
		}
		feeds = append(feeds, f)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, f := range feeds {
		bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
		result, fetchErr := s.collector.FetchFeed(bounded, f.source, f.etag, f.modified)
		cancel()
		tx, err := s.db.Begin(ctx)
		if err != nil {
			return err
		}
		if err = lock(ctx, tx); err != nil {
			tx.Rollback(ctx)
			return err
		}
		var owns bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM autonomy_state WHERE user_id=$1 AND collect_token=$2 AND NOT paused)`, memory.UserID, token).Scan(&owns)
		if err != nil {
			tx.Rollback(ctx)
			return err
		}
		if !owns {
			tx.Rollback(ctx)
			return nil
		}
		state, code := "ok", ""
		if fetchErr != nil {
			state, code = "failed", "source_unavailable"
		} else if result.NotModified {
			state = "unchanged"
		}
		_, err = tx.Exec(ctx, `UPDATE autonomy_sources SET state=$2,error_code=$3,last_checked_at=$4,last_success_at=CASE WHEN $3='' THEN $4 ELSE last_success_at END,etag=CASE WHEN $3='' THEN $5 ELSE etag END,last_modified=CASE WHEN $3='' THEN $6 ELSE last_modified END WHERE id=$1`, f.source.ID, state, code, now, result.ETag, result.LastModified)
		if err == nil && fetchErr == nil {
			for _, doc := range result.Documents {
				if _, err = saveDocument(ctx, tx, f.source.ID, doc, now); err != nil {
					break
				}
			}
		}
		if err == nil {
			err = tx.Commit(ctx)
		} else {
			tx.Rollback(ctx)
		}
		if err != nil {
			return err
		}
	}
	_, err = s.db.Exec(ctx, `UPDATE autonomy_state SET collect_token=NULL,collect_expires_at=NULL WHERE user_id=$1 AND collect_token=$2`, memory.UserID, token)
	return err
}
func saveDocument(ctx context.Context, tx pgx.Tx, source string, doc research.Document, at time.Time) (string, error) {
	if doc.URL == "" || doc.Title == "" {
		return "", fmt.Errorf("invalid public document")
	}
	if !doc.CollectedAt.IsZero() {
		at = doc.CollectedAt
	}
	hash := doc.Hash
	if hash == "" {
		hash = digest(doc.URL + "|" + doc.Title + "|" + doc.Summary)
	}
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO autonomy_evidence(id,source_id,url,title,summary,kind,content_hash,published_at,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(url,content_hash) DO NOTHING RETURNING id::text`, memory.ID(), source, doc.URL, doc.Title, doc.Summary, doc.Kind, hash, doc.PublishedAt, at).Scan(&id)
	if err == pgx.ErrNoRows {
		err = tx.QueryRow(ctx, `SELECT id::text FROM autonomy_evidence WHERE url=$1 AND content_hash=$2`, doc.URL, hash).Scan(&id)
	}
	if err != nil {
		return "", err
	}
	var isFeed bool
	if err = tx.QueryRow(ctx, `SELECT is_feed FROM autonomy_sources WHERE id=$1`, source).Scan(&isFeed); err != nil {
		return "", err
	}
	var previous string
	var previousAt time.Time
	err = tx.QueryRow(ctx, `SELECT evidence_id::text,observed_at FROM autonomy_evidence_observations WHERE source_id=$1 AND url=$2 ORDER BY observed_at DESC,id DESC LIMIT 1`, source, doc.URL).Scan(&previous, &previousAt)
	if err != nil && err != pgx.ErrNoRows {
		return "", err
	}
	if previous == id && (isFeed || previousAt.Equal(at)) {
		return id, nil
	}
	// Content stays immutable and deduplicated. Record feed transitions and
	// every verified page observation separately, including A -> B -> A.
	_, err = tx.Exec(ctx, `INSERT INTO autonomy_evidence_observations(evidence_id,source_id,url,observed_at) VALUES($1,$2,$3,$4)`, id, source, doc.URL, at)
	return id, err
}
