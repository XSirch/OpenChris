package autonomy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"openchris/conselho/internal/codex"
	"openchris/conselho/internal/memory"
	"openchris/conselho/internal/research"
)

type collector interface {
	FetchFeed(context.Context, research.Source, string, string) (research.FeedResult, error)
	VerifyDocument(context.Context, string) (research.Document, error)
}
type Service struct {
	db        *pgxpool.Pool
	provider  Provider
	builder   Builder
	cfg       Config
	collector collector
	now       func() time.Time
}

const minute = time.Minute

func New(db *pgxpool.Pool, provider Provider, builder Builder, cfg Config) *Service {
	if cfg.DailyLimit <= 0 || cfg.DailyLimit > 30 {
		cfg.DailyLimit = 30
	}
	if cfg.AutoDailyLimit <= 0 || cfg.AutoDailyLimit > 20 {
		cfg.AutoDailyLimit = 20
	}
	if cfg.AutoDailyLimit > cfg.DailyLimit {
		cfg.AutoDailyLimit = cfg.DailyLimit
	}
	return &Service{db: db, provider: provider, builder: builder, cfg: cfg, collector: research.NewClient(codex.ResearchDomains()), now: func() time.Time { return time.Now().UTC() }}
}
func lock(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(20261027)`)
	return err
}
func ensure(ctx context.Context, tx pgx.Tx, at time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO autonomy_state(user_id,next_collect_at,next_run_at,notice_epoch) VALUES($1,$2,$2,$2) ON CONFLICT DO NOTHING`, memory.UserID, at)
	return err
}
func digest(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }

// Run keeps supervision independent from an inference already in flight.
func (s *Service) Run(ctx context.Context, onError func(error)) {
	if !s.cfg.Enabled {
		return
	}
	report := func(err error) {
		if err != nil && ctx.Err() == nil && onError != nil {
			onError(err)
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, err := s.ProcessOne(ctx)
				report(err)
			}
		}
	}()
	report(s.Tick(ctx, s.now()))
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			<-done
			return
		case <-ticker.C:
			report(s.Tick(ctx, s.now()))
		}
	}
}
