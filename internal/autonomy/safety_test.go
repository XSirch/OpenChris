package autonomy

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"openchris/conselho/internal/codex"
	"openchris/conselho/internal/research"
)

func TestInitialCollectionAndVersionsAreImmutable(t *testing.T) {
	s, store, _, _ := fixture(t)
	at := time.Date(2026, 11, 1, 15, 0, 0, 0, time.UTC)
	pub := at.Add(-time.Hour)
	c := &fakeCollector{docs: []research.Document{{URL: "https://www.bcb.gov.br/fixture", Title: "Nota pública", Summary: "Primeira versão", Kind: "primary", Hash: "v1", PublishedAt: &pub}}}
	s.collector = c
	tick(t, s, at)
	if c.calls != len(research.DefaultFeeds()) {
		t.Fatal("initial collection missing", c.calls)
	}
	tick(t, s, at.Add(14*time.Minute))
	if c.calls != len(research.DefaultFeeds()) {
		t.Fatal("collected too early")
	}
	tick(t, s, at.Add(15*time.Minute))
	var count int
	store.DB.QueryRow(context.Background(), `SELECT count(*) FROM autonomy_evidence`).Scan(&count)
	if count != 1 {
		t.Fatal("duplicate version", count)
	}
	c.docs[0].Summary = "Segunda versão"
	c.docs[0].Hash = "v2"
	tick(t, s, at.Add(30*time.Minute))
	store.DB.QueryRow(context.Background(), `SELECT count(*) FROM autonomy_evidence`).Scan(&count)
	if count != 2 {
		t.Fatal("lost content version", count)
	}
	detail, err := s.RunDetail(context.Background(), mustRun(t, s))
	if err != nil || len(detail.Evidence) != 1 || detail.Evidence[0].Summary != "Primeira versão" {
		t.Fatal("selected evidence mutated", detail.Evidence, err)
	}
}
func mustRun(t *testing.T, s *Service) string {
	t.Helper()
	st, err := s.Status(context.Background())
	if err != nil || len(st.Runs) == 0 {
		t.Fatal(st, err)
	}
	return st.Runs[0].ID
}

func TestExpiredLeaseNeverRepeatsUnknownResult(t *testing.T) {
	s, store, p, _ := fixture(t)
	at := time.Now().UTC()
	tick(t, s, at)
	p.research = func(ctx context.Context, in codex.ResearchRequest) (codex.ResearchResult, error) {
		_, err := store.DB.Exec(ctx, `UPDATE autonomy_tasks SET lease_expires_at=$1 WHERE status='running'`, at.Add(-time.Second))
		return codex.ResearchResult{}, err
	}
	process(t, s)
	tick(t, s, at.Add(time.Minute))
	ok, err := s.ProcessOne(context.Background())
	if err != nil || ok || p.calls.Load() != 1 {
		t.Fatal("unknown inference repeated", ok, err, p.calls.Load())
	}
	st, _ := s.Status(context.Background())
	if st.Runs[0].Status != "partial" || st.Tasks[0].Status != "failed_unknown" {
		t.Fatal(st.Runs, st.Tasks)
	}
}
func TestConcurrentAdmissionAndHeartbeatDuringInference(t *testing.T) {
	s, _, p, _ := fixture(t)
	at := time.Now().UTC()
	tick(t, s, at)
	started, release := make(chan struct{}), make(chan struct{})
	p.research = func(context.Context, codex.ResearchRequest) (codex.ResearchResult, error) {
		close(started)
		<-release
		return codex.ResearchResult{Summary: "Pesquisa", Sources: []codex.ResearchSource{}, Unknowns: []string{}, Model: codex.DefaultModel}, nil
	}
	done := make(chan error, 1)
	go func() { _, err := s.ProcessOne(context.Background()); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("did not start")
	}
	ok, err := s.ProcessOne(context.Background())
	if err != nil || ok {
		close(release)
		t.Fatal(ok, err)
	}
	// Tick performs no inference and can update its heartbeat while the call waits.
	if err = s.Tick(context.Background(), at.Add(time.Minute)); err != nil {
		close(release)
		t.Fatal(err)
	}
	st, _ := s.Status(context.Background())
	if st.HeartbeatAt == nil || st.HeartbeatAt.Before(at.Add(59*time.Second)) {
		close(release)
		t.Fatal("heartbeat blocked")
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if p.calls.Load() != 1 {
		t.Fatal(p.calls.Load())
	}
}
func TestQuota20Auto30GlobalAndSaoPauloReset(t *testing.T) {
	for _, tc := range []struct {
		name, origin string
		count        int
	}{{"auto", "autonomy", 20}, {"global", "conversation", 30}} {
		t.Run(tc.name, func(t *testing.T) {
			s, store, p, _ := fixture(t)
			at := time.Now().UTC()
			tick(t, s, at)
			_, err := store.DB.Exec(context.Background(), `INSERT INTO council_attempts(run_id,origin,quota_day) SELECT md5(n::text)::uuid,$1,($2::timestamptz AT TIME ZONE 'America/Sao_Paulo')::date FROM generate_series(1,$3::int)n`, tc.origin, at, tc.count)
			if err != nil {
				t.Fatal(err)
			}
			if ok, err := s.ProcessOne(context.Background()); ok || err != nil || p.calls.Load() != 0 {
				t.Fatal("quota exceeded", ok, err, p.calls.Load())
			}
			loc, _ := time.LoadLocation("America/Sao_Paulo")
			next := at.In(loc).AddDate(0, 0, 1)
			next = time.Date(next.Year(), next.Month(), next.Day(), 0, 0, 0, 0, loc)
			s.now = func() time.Time { return next }
			process(t, s)
			st, _ := s.Status(context.Background())
			if st.Attempts != 1 || st.AutoAttempts != 1 {
				t.Fatal("reset ignored SP", st.Attempts, st.AutoAttempts)
			}
		})
	}
}
func TestFailureAndPauseDuringInferenceNeverPublish(t *testing.T) {
	for _, kind := range []string{"failed_unknown", "pause", "changed"} {
		t.Run(kind, func(t *testing.T) {
			s, store, p, b := fixture(t)
			tick(t, s, time.Now().UTC())
			process(t, s)
			p.run = func(ctx context.Context, in codex.Request) (codex.Result, error) {
				switch kind {
				case "pause":
					if err := s.SetPaused(ctx, true); err != nil {
						t.Fatal(err)
					}
				case "changed":
					b.fingerprint = "changed"
				case "failed_unknown":
					return codex.Result{}, &codex.Error{Kind: "failed_unknown"}
				}
				return answer(in), nil
			}
			process(t, s)
			var count int
			store.DB.QueryRow(context.Background(), `SELECT count(*) FROM autonomy_tasks WHERE stage=1 AND result IS NOT NULL`).Scan(&count)
			if count != 0 {
				t.Fatal("published invalid result")
			}
			if ok, err := s.ProcessOne(context.Background()); err != nil || ok {
				t.Fatal("chained invalid result", ok, err)
			}
			strategies, _ := s.Strategies(context.Background())
			if len(strategies) != 0 {
				t.Fatal("published strategy")
			}
		})
	}
}
func TestSourceFailureIsVisibleAndDoesNotBecomeNoNews(t *testing.T) {
	s, _, _, _ := fixture(t)
	s.collector = &fakeCollector{err: errors.New("secret provider failure")}
	tick(t, s, time.Now().UTC())
	st, _ := s.Status(context.Background())
	if len(st.Sources) == 0 || st.Sources[0].State != "failed" || st.Sources[0].ErrorCode != "source_unavailable" {
		t.Fatal(st.Sources)
	}
}

func TestStrategyMemoryAndVersionSurviveNextRound(t *testing.T) {
	s, _, _, b := fixture(t)
	at := time.Now().UTC()
	tick(t, s, at)
	for i := 0; i < 5; i++ {
		process(t, s)
	}
	tick(t, s, at.Add(6*time.Hour))
	for i := 0; i < 5; i++ {
		process(t, s)
	}
	if len(b.inputs) != 8 || len(b.inputs[4].Strategies) != 1 {
		t.Fatal("memory missing")
	}
	strategies, _ := s.Strategies(context.Background())
	var out map[string]any
	json.Unmarshal(strategies[0], &out)
	if out["version"] != float64(2) {
		t.Fatal(out)
	}
}

func TestConcurrentTicksCreateOnlyOneRound(t *testing.T) {
	s, _, _, _ := fixture(t)
	at := time.Now().UTC()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.Tick(context.Background(), at) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	st, _ := s.Status(context.Background())
	if len(st.Runs) != 1 {
		t.Fatal("duplicate runs", len(st.Runs))
	}
}

func TestChangedSelectedEvidenceInvalidatesPrivateResult(t *testing.T) {
	s, store, _, _ := fixture(t)
	at := time.Now().UTC()
	published := at.Add(-time.Hour)
	c := &fakeCollector{docs: []research.Document{{URL: "https://www.bcb.gov.br/fixture", Title: "Nota", Summary: "Original", Kind: "primary", Hash: "original", PublishedAt: &published}}}
	s.collector = c
	tick(t, s, at)
	process(t, s)
	c.docs[0].Hash = "corrected"
	c.docs[0].Summary = "Corrigida"
	tick(t, s, at.Add(15*time.Minute))
	process(t, s)
	var count int
	store.DB.QueryRow(context.Background(), `SELECT count(*) FROM autonomy_tasks WHERE stage=1 AND result IS NOT NULL`).Scan(&count)
	if count != 0 {
		t.Fatal("published superseded selected evidence")
	}
	st, _ := s.Status(context.Background())
	if st.Runs[0].Status != "stale" {
		t.Fatal(st.Runs)
	}
}

func TestQuietHoursRecheckedForPendingNotices(t *testing.T) {
	s, _, _, _ := fixture(t)
	loc, _ := time.LoadLocation("America/Sao_Paulo")
	at := time.Date(2026, 11, 8, 9, 0, 0, 0, loc)
	tick(t, s, at)
	for i := 0; i < 5; i++ {
		process(t, s)
	}
	notices, err := s.PendingNotices(context.Background())
	if err != nil || len(notices) != 1 {
		t.Fatal(notices, err)
	}
	s.now = func() time.Time { return time.Date(2026, 11, 8, 22, 0, 0, 0, loc) }
	notices, err = s.PendingNotices(context.Background())
	if err != nil || len(notices) != 0 {
		t.Fatal("queued notice escaped quiet hours", notices, err)
	}
}

func TestSummarySlotsDeduplicateAndResumeDropsOldNotices(t *testing.T) {
	s, _, _, _ := fixture(t)
	ctx := context.Background()
	loc, _ := time.LoadLocation("America/Sao_Paulo")
	at := time.Date(2026, 11, 9, 7, 59, 0, 0, loc)
	tick(t, s, at)
	for i := 0; i < 5; i++ {
		process(t, s)
	}
	notices, err := s.PendingNotices(ctx)
	if err != nil || len(notices) != 0 {
		t.Fatal("summary before08", notices, err)
	}
	tick(t, s, at.Add(time.Minute))
	notices, err = s.PendingNotices(ctx)
	if err != nil || len(notices) != 1 {
		t.Fatal("08 summary missing", notices, err)
	}
	tick(t, s, at.Add(2*time.Minute))
	again, _ := s.PendingNotices(ctx)
	if len(again) != 1 || again[0].ID != notices[0].ID {
		t.Fatal("duplicate summary", again)
	}
	tick(t, s, time.Date(2026, 11, 9, 19, 0, 0, 0, loc))
	for i := 0; i < 5; i++ {
		process(t, s)
	}
	notices, _ = s.PendingNotices(ctx)
	if len(notices) != 1 {
		t.Fatal("duplicate evening summary", notices)
	}
	if _, err = s.RequestRun(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		process(t, s)
	}
	notices, _ = s.PendingNotices(ctx)
	if len(notices) != 1 {
		t.Fatal("more than one daily summary", notices)
	}
	if err = s.SetPaused(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err = s.SetPaused(ctx, false); err != nil {
		t.Fatal(err)
	}
	notices, err = s.PendingNotices(ctx)
	if err != nil || len(notices) != 0 {
		t.Fatal("old notices replayed on resume", notices, err)
	}
}

func TestTransientBusyRetriesAtMostTwiceAndConsumesAttempts(t *testing.T) {
	s, _, p, _ := fixture(t)
	at := time.Now().UTC()
	tick(t, s, at)
	p.research = func(context.Context, codex.ResearchRequest) (codex.ResearchResult, error) {
		return codex.ResearchResult{}, &codex.Error{Kind: "busy", Transient: true}
	}
	process(t, s)
	if ok, err := s.ProcessOne(context.Background()); ok || err != nil {
		t.Fatal("busy retried before delay", ok, err)
	}
	s.now = func() time.Time { return at.Add(2 * time.Second) }
	process(t, s)
	if ok, err := s.ProcessOne(context.Background()); ok || err != nil {
		t.Fatal("third retry", ok, err)
	}
	st, _ := s.Status(context.Background())
	if st.AutoAttempts != 2 || st.Attempts != 2 || p.calls.Load() != 2 || st.Runs[0].Status != "partial" {
		t.Fatal(st.Runs, st.Attempts, st.AutoAttempts, p.calls.Load())
	}
}

func TestActivityEventsTrackCommittedTransitions(t *testing.T) {
	s, store, _, _ := fixture(t)
	ctx := context.Background()
	before, err := store.Snapshot(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	cursor := before.Cursor
	check := func(want int) {
		t.Helper()
		events, err := store.Events(ctx, cursor)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, event := range events {
			cursor = event.Sequence
			if event.Type == "autonomy.changed" {
				count++
				if string(event.Payload) != "{}" {
					t.Fatalf("activity event exposed payload: %s", event.Payload)
				}
			}
		}
		if count != want {
			t.Fatalf("activity events=%d want=%d", count, want)
		}
	}
	tick(t, s, time.Now().UTC())
	check(1)
	for i := 0; i < 2; i++ {
		if _, ok, err := s.claim(ctx, "not_authenticated"); ok || err != nil {
			t.Fatal("unavailable runner claimed work", ok, err)
		}
	}
	check(1)
	task, claimed, err := s.claim(ctx, "available")
	if err != nil || !claimed {
		t.Fatal("claim", claimed, err)
	}
	check(1)
	st, err := s.Status(ctx)
	if err != nil || st.Tasks[0].Status != "running" || st.Tasks[0].LeaseExpiresAt == nil {
		t.Fatal("status does not expose the running lease", err)
	}
	if err := s.SetPaused(ctx, true); err != nil {
		t.Fatal(err)
	}
	check(1)
	tx, err := store.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT 1/0`); err == nil {
		t.Fatal("expected failed transaction")
	}
	if err := commitActivity(ctx, tx, true); err == nil {
		t.Fatal("failed transaction published activity")
	}
	_ = tx.Rollback(ctx)
	check(0)
	if err := s.finish(ctx, task, codex.Result{}, codex.ResearchResult{}, nil, &codex.Error{Kind: "paused"}); err != nil {
		t.Fatal(err)
	}
	check(1)
}
