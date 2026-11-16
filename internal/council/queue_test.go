package council

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"openchris/conselho/internal/codex"
	"openchris/conselho/internal/memory"
)

type fakeProvider struct {
	calls atomic.Int32
	run   func(context.Context, codex.Request) (codex.Result, error)
}

func (f *fakeProvider) Status(context.Context) (codex.Status, error) {
	return codex.Status{State: "available", Model: codex.DefaultModel}, nil
}
func (f *fakeProvider) Run(ctx context.Context, in codex.Request) (codex.Result, error) {
	f.calls.Add(1)
	if f.run != nil {
		return f.run(ctx, in)
	}
	return answer(in), nil
}
func answer(in codex.Request) codex.Result {
	var c struct {
		Sources []struct {
			ID string `json:"id"`
		} `json:"sources"`
	}
	json.Unmarshal(in.Context, &c)
	refs := []string{}
	if len(c.Sources) > 0 {
		refs = append(refs, c.Sources[0].ID)
	}
	return codex.Result{Reply: "Análise de teste. Consulte as fontes e as limitações.", Episode: "Conversa analisada.", Evidence: refs, Assumptions: []string{}, Unknowns: []string{}, Model: codex.DefaultModel}
}
func fixture(t *testing.T, limit int) (*Service, *memory.Store, *fakeProvider) {
	t.Helper()
	dsn := os.Getenv("COUNCIL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("COUNCIL_TEST_DATABASE_URL must name isolated conselho_council_test")
	}
	store, err := memory.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	if store.DB.Config().ConnConfig.Database != "conselho_council_test" {
		t.Fatal("refusing cleanup outside isolated test DB")
	}
	_, err = store.DB.Exec(context.Background(), `TRUNCATE conversation_events,finance_transactions,finance_net_worth,finance_audit RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{}
	return New(store.DB, provider, Config{Enabled: true, PublicURL: "https://conselho.example.test", DailyLimit: limit}), store, provider
}
func ingest(t *testing.T, s *Service, channel, key, text string) memory.Receipt {
	t.Helper()
	receipt, err := s.Ingest(context.Background(), memory.Input{Channel: channel, Key: key, Text: text})
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}
func process(t *testing.T, s *Service) {
	t.Helper()
	ok, err := s.ProcessOne(context.Background())
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
}

func TestDurableIdempotentAnalysisAcrossChannels(t *testing.T) {
	s, store, provider := fixture(t, 30)
	first := ingest(t, s, "dashboard", "key-first", "Minha reserva é de 6 meses")
	duplicate := ingest(t, s, "dashboard", "key-first", "Minha reserva é de 6 meses")
	if !duplicate.Duplicate || duplicate.ID != first.ID {
		t.Fatal(duplicate)
	}
	if _, err := s.Ingest(context.Background(), memory.Input{Channel: "dashboard", Key: "key-first", Text: "outro"}); err != memory.ErrConflict {
		t.Fatal(err)
	}
	second := ingest(t, s, "telegram", "key-second", "Cora, como organizar meus gastos?")
	provider.run = func(ctx context.Context, in codex.Request) (codex.Result, error) {
		var lockFree bool
		if err := store.DB.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(20261027)`).Scan(&lockFree); err != nil || !lockFree {
			t.Fatal("inference held writer transaction", err)
		}
		if provider.calls.Load() == 2 && (!strings.Contains(string(in.Context), "reserve_months") || in.AgentID != "A05") {
			t.Fatal("missing shared context or wrong profile", in.AgentID)
		}
		return answer(in), nil
	}
	process(t, s)
	process(t, s)
	if provider.calls.Load() != 2 {
		t.Fatal(provider.calls.Load())
	}
	snap, err := store.Snapshot(context.Background(), "")
	if err != nil || len(snap.Messages) != 4 || len(snap.Facts) != 1 || snap.Facts[0].Value != "6" {
		t.Fatalf("snapshot %+v err %v", snap, err)
	}
	var count int
	store.DB.QueryRow(context.Background(), `SELECT count(*) FROM council_results WHERE source_event_id IN ($1,$2) AND envelope->>'schema_version'='agent.result.v2' AND envelope->'usage'->'input_tokens'='null'::jsonb`, first.ID, second.ID).Scan(&count)
	if count != 2 {
		t.Fatal("unknown tokens became zero or missing envelope", count)
	}
}

func TestUnknownResultNeverRetriesAndNextMessageCanProceed(t *testing.T) {
	s, store, p := fixture(t, 30)
	first := ingest(t, s, "dashboard", "unknown-1", "Analise minha situação")
	ingest(t, s, "dashboard", "unknown-2", "Olá, Atlas")
	p.run = func(_ context.Context, in codex.Request) (codex.Result, error) {
		if p.calls.Load() == 1 {
			return codex.Result{}, &codex.Error{Kind: "failed_unknown"}
		}
		return answer(in), nil
	}
	process(t, s)
	process(t, s)
	if p.calls.Load() != 2 {
		t.Fatal(p.calls.Load())
	}
	var state string
	store.DB.QueryRow(context.Background(), `SELECT status FROM council_jobs WHERE event_id=$1`, first.ID).Scan(&state)
	if state != "failed_unknown" {
		t.Fatal(state)
	}
	if ok, err := s.ProcessOne(context.Background()); err != nil || ok {
		t.Fatal("retried unknown inference", ok, err)
	}
}

func TestExpiredLeaseDiscardsLateResult(t *testing.T) {
	s, store, p := fixture(t, 30)
	receipt := ingest(t, s, "dashboard", "expired-lease", "Olá")
	p.run = func(ctx context.Context, in codex.Request) (codex.Result, error) {
		_, err := store.DB.Exec(ctx, `UPDATE council_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE event_id=$1`, receipt.ID)
		if err != nil {
			t.Fatal(err)
		}
		return answer(in), nil
	}
	process(t, s)
	s.ProcessOne(context.Background())
	var results int
	store.DB.QueryRow(context.Background(), `SELECT count(*) FROM council_results`).Scan(&results)
	if results != 0 || p.calls.Load() != 1 {
		t.Fatal("late publication or repeated call", results, p.calls.Load())
	}
	var state string
	store.DB.QueryRow(context.Background(), `SELECT status FROM council_jobs WHERE event_id=$1`, receipt.ID).Scan(&state)
	if state != "failed_unknown" {
		t.Fatal(state)
	}
}

func TestDailyQuotaCountsAttemptsWithoutDroppingMessages(t *testing.T) {
	s, store, p := fixture(t, 1)
	ingest(t, s, "dashboard", "quota-1", "Olá")
	second := ingest(t, s, "dashboard", "quota-2", "Outra pergunta")
	process(t, s)
	process(t, s)
	if p.calls.Load() != 1 {
		t.Fatal("quota exceeded")
	}
	var status string
	store.DB.QueryRow(context.Background(), `SELECT status FROM council_jobs WHERE event_id=$1`, second.ID).Scan(&status)
	if status != "blocked" {
		t.Fatal(status)
	}
	snap, _ := store.Snapshot(context.Background(), "")
	if len(snap.Messages) != 3 {
		t.Fatal("journal lost", len(snap.Messages))
	}
}

func TestMaterialChangeMakesAnalysisStale(t *testing.T) {
	s, store, p := fixture(t, 30)
	receipt := ingest(t, s, "dashboard", "stale-1", "Analise o patrimônio")
	p.run = func(ctx context.Context, in codex.Request) (codex.Result, error) {
		_, err := store.DB.Exec(ctx, `INSERT INTO finance_audit(user_id,entity_type,entity_id,version,action,payload) VALUES($1,'category',$2,1,'created','{}')`, memory.UserID, memory.ID())
		if err != nil {
			t.Fatal(err)
		}
		return answer(in), nil
	}
	process(t, s)
	var state string
	store.DB.QueryRow(context.Background(), `SELECT status FROM council_jobs WHERE event_id=$1`, receipt.ID).Scan(&state)
	if state != "stale" {
		t.Fatal(state)
	}
	var count int
	store.DB.QueryRow(context.Background(), `SELECT count(*) FROM conversation_events WHERE direction='outgoing'`).Scan(&count)
	if count != 0 {
		t.Fatal("published stale result")
	}
}

func TestEarlierAuditIDCommittingLateStillInvalidatesContext(t *testing.T) {
	s, store, p := fixture(t, 30)
	ctx := context.Background()
	earlier, err := store.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer earlier.Rollback(ctx)
	insert := `INSERT INTO finance_audit(user_id,entity_type,entity_id,version,action,payload) VALUES($1,'category',$2,1,'created','{}')`
	if _, err = earlier.Exec(ctx, insert, memory.UserID, memory.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB.Exec(ctx, insert, memory.UserID, memory.ID()); err != nil {
		t.Fatal(err)
	}
	receipt := ingest(t, s, "dashboard", "late-commit", "Analise os dados manuais")
	p.run = func(ctx context.Context, in codex.Request) (codex.Result, error) {
		if err := earlier.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return answer(in), nil
	}
	process(t, s)
	var state string
	store.DB.QueryRow(ctx, `SELECT status FROM council_jobs WHERE event_id=$1`, receipt.ID).Scan(&state)
	if state != "stale" {
		t.Fatal("missed material commit below maximum audit ID", state)
	}
}

func TestContextRedactsSecretsAndNeverIncludesTransactions(t *testing.T) {
	s, _, p := fixture(t, 30)
	ingest(t, s, "dashboard", "privacy-1", "Meu CPF é 123.456.789-00. Token sk-abcdefghijklmnopqrstuv")
	p.run = func(_ context.Context, in codex.Request) (codex.Result, error) {
		for _, sensitive := range []string{"123.456.789-00", "sk-abcdefghijklmnopqrstuv", "DATABASE_URL"} {
			if strings.Contains(string(in.Context), sensitive) {
				t.Fatal("sensitive context", sensitive)
			}
		}
		return answer(in), nil
	}
	process(t, s)
}

func TestConcurrentWorkerDoesNotStartSecondInference(t *testing.T) {
	s, _, p := fixture(t, 30)
	ingest(t, s, "dashboard", "concurrency-1", "Olá")
	ingest(t, s, "dashboard", "concurrency-2", "Olá de novo")
	started, release := make(chan struct{}), make(chan struct{})
	p.run = func(_ context.Context, in codex.Request) (codex.Result, error) {
		close(started)
		<-release
		return answer(in), nil
	}
	done := make(chan error, 1)
	go func() { _, err := s.ProcessOne(context.Background()); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not start")
	}
	ok, err := s.ProcessOne(context.Background())
	if err != nil || ok || p.calls.Load() != 1 {
		t.Fatal(fmt.Sprint(ok, err, p.calls.Load()))
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
