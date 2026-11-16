package pluggy

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"openchris/conselho/internal/memory"
)

// Run is a durable pull worker. Stale leases are reclaimed after a crash, while
// short transient failures retry with bounded backoff before dead lettering.
func (s *Service) Run(ctx context.Context, onError func(error)) {
	if s.ready() != nil {
		return
	}
	report := func(err error) {
		if err != nil && !errors.Is(err, ErrBusy) && ctx.Err() == nil && onError != nil {
			onError(err)
		}
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastEnqueue := time.Time{}
	for {
		if ctx.Err() != nil {
			return
		}
		if time.Since(lastEnqueue) >= time.Minute {
			report(s.enqueueDue(ctx))
			lastEnqueue = time.Now()
		}
		did, err := s.processOne(ctx)
		report(err)
		if did {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Service) enqueueDue(ctx context.Context) error {
	if err := s.storage(); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, `INSERT INTO pluggy_jobs(user_id,item_id) SELECT user_id,item_id FROM pluggy_items WHERE user_id=$1 AND next_sync_at<=clock_timestamp() AND superseded_by IS NULL AND status NOT IN ('revoked','expired','user_action_required') ON CONFLICT DO NOTHING`, memory.UserID)
	return err
}
func (s *Service) processOne(ctx context.Context) (bool, error) {
	if err := s.storage(); err != nil {
		return false, err
	}
	var id int64
	var itemID string
	var eventID *string
	var discover bool
	var attempts int
	err := s.db.QueryRow(ctx, `WITH due AS (SELECT id FROM pluggy_jobs WHERE user_id=$1 AND ((status='pending' AND available_at<=clock_timestamp()) OR (status='running' AND locked_at<clock_timestamp()-interval '10 minutes')) ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE pluggy_jobs j SET status='running',locked_at=clock_timestamp(),attempts=j.attempts+1 FROM due WHERE j.id=due.id RETURNING j.id,j.item_id::text,j.event_id::text,j.discover,j.attempts`, memory.UserID).Scan(&id, &itemID, &eventID, &discover, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if discover {
		err = s.register(ctx, itemID, false)
	}
	if err == nil {
		err = s.SyncItem(ctx, itemID)
	}
	// Context cancellation leaves the durable running lease for the next worker.
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	state := "complete"
	inboxState := "processed"
	var code any
	delay := time.Duration(0)
	if err != nil {
		code = failureCode(err)
		state = "pending"
		inboxState = "pending"
		delay = time.Duration(1<<min(attempts, 10)) * 30 * time.Second
		var api *APIError
		if errors.As(err, &api) && api.RetryAfter > delay {
			delay = api.RetryAfter
		}
		if attempts >= 6 || errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrInvalid) || errors.Is(err, ErrCapacity) {
			state = "failed"
			inboxState = "failed"
		}
		if errors.Is(err, ErrBusy) {
			// Contention is scheduling, not a provider failure. Preserve the job
			// and its failure budget while the other replica finishes this item.
			state = "pending"
			inboxState = "pending"
			delay = 15 * time.Second
		}
		if errors.Is(err, errItemSuperseded) {
			// The renewed item owns the records now. This obsolete consent has
			// finished its work and must not retry against its replacement.
			state = "complete"
			inboxState = "processed"
			code = "CONSENT_REPLACED"
			delay = 0
		}
	}
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return true, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `UPDATE pluggy_jobs SET status=$2,last_error_code=$3,available_at=clock_timestamp()+$4::bigint*interval '1 millisecond',attempts=CASE WHEN $5 THEN greatest(attempts-1,0) ELSE attempts END,finished_at=CASE WHEN $2 IN ('complete','failed') THEN clock_timestamp() ELSE NULL END WHERE id=$1`, id, state, code, delay.Milliseconds(), errors.Is(err, ErrBusy)); e != nil {
		return true, e
	}
	if eventID != nil {
		if _, e = tx.Exec(ctx, `UPDATE pluggy_inbox SET status=$2,processed_at=CASE WHEN $2 IN ('processed','failed') THEN clock_timestamp() ELSE NULL END WHERE event_id=$1`, *eventID, inboxState); e != nil {
			return true, e
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return true, e
	}
	if errors.Is(err, errItemSuperseded) {
		return true, nil
	}
	return true, err
}
