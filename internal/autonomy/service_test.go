package autonomy

import (
	"context"
	"encoding/json"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"openchris/conselho/internal/codex"
	"openchris/conselho/internal/finance"
	"openchris/conselho/internal/memory"
	"openchris/conselho/internal/research"
)

type fakeProvider struct {
	calls    atomic.Int32
	run      func(context.Context, codex.Request) (codex.Result, error)
	research func(context.Context, codex.ResearchRequest) (codex.ResearchResult, error)
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
func (f *fakeProvider) Research(ctx context.Context, in codex.ResearchRequest) (codex.ResearchResult, error) {
	f.calls.Add(1)
	if f.research != nil {
		return f.research(ctx, in)
	}
	return codex.ResearchResult{Summary: "Pesquisa pública", Sources: []codex.ResearchSource{}, Unknowns: []string{}, Model: codex.DefaultModel}, nil
}
func answer(in codex.Request) codex.Result {
	return codex.Result{Reply: "Estudo condicionado a dados.", Episode: "Análise registrada.", Evidence: []string{}, Assumptions: []string{}, Unknowns: []string{"Perfil de risco ausente"}, Model: codex.DefaultModel, Strategy: &codex.StrategyDraft{Title: "Estudo de prazo", Hypothesis: "Prazo deve refletir o objetivo", Mandate: "long_term", TimeHorizon: "longo prazo", Conditions: []string{}, InvalidationConditions: []string{}, EligibleInstruments: []string{}, Risks: []string{"Perfil desconhecido"}, Evidence: []string{}}}
}

type fakeBuilder struct {
	fingerprint string
	inputs      []ContextInput
	validUntil  time.Time
	personal    *finance.PersonalFinance
}

func (b *fakeBuilder) Fingerprint(_ context.Context, _ pgx.Tx, at time.Time) (string, error) {
	if !b.validUntil.IsZero() && !at.Before(b.validUntil) {
		return "expired", nil
	}
	return b.fingerprint, nil
}
func (b *fakeBuilder) Build(_ context.Context, _ pgx.Tx, in ContextInput) (ContextSnapshot, error) {
	b.inputs = append(b.inputs, in)
	raw, _ := json.Marshal(map[string]any{"sources": in.Evidence, "prior": in.Prior, "strategies": in.Strategies, "personal_finance": b.personal})
	return ContextSnapshot{Context: raw, Fingerprint: b.fingerprint}, nil
}

type fakeCollector struct {
	calls int
	err   error
	docs  []research.Document
}

func (c *fakeCollector) FetchFeed(context.Context, research.Source, string, string) (research.FeedResult, error) {
	c.calls++
	return research.FeedResult{Documents: c.docs, ETag: "fixture"}, c.err
}
func (c *fakeCollector) VerifyDocument(context.Context, string) (research.Document, error) {
	return research.Document{}, c.err
}
func fixture(t *testing.T) (*Service, *memory.Store, *fakeProvider, *fakeBuilder) {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("AUTONOMY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("AUTONOMY_TEST_DATABASE_URL must name isolated conselho_autonomy_test")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid isolated autonomy test database configuration")
	}
	if config.Database != "conselho_autonomy_test" {
		t.Fatal("refusing migrations and cleanup outside exact isolated database")
	}
	store, err := memory.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	if store.DB.Config().ConnConfig.Database != "conselho_autonomy_test" {
		t.Fatal("refusing cleanup outside exact isolated database")
	}
	_, err = store.DB.Exec(ctx, `TRUNCATE autonomy_state,autonomy_sources,autonomy_runs,autonomy_strategies,conversation_events,council_attempts,pluggy_items,finance_profile,finance_classification_rules CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{}
	b := &fakeBuilder{fingerprint: "fixture-v1"}
	s := New(store.DB, p, b, Config{Enabled: true, DailyLimit: 30, AutoDailyLimit: 20})
	s.collector = &fakeCollector{}
	return s, store, p, b
}
func tick(t *testing.T, s *Service, at time.Time) {
	t.Helper()
	s.now = func() time.Time { return at }
	if err := s.Tick(context.Background(), at); err != nil {
		t.Fatal(err)
	}
}
func process(t *testing.T, s *Service) {
	t.Helper()
	ok, err := s.ProcessOne(context.Background())
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
}

func TestInitialRoundCompletesFiveStagesAndCriticIsIndependent(t *testing.T) {
	s, store, p, b := fixture(t)
	now := time.Now().UTC()
	tick(t, s, now)
	for i := 0; i < 5; i++ {
		process(t, s)
	}
	status, err := s.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Runs) != 1 || status.Runs[0].Status != "await_data" || p.calls.Load() != 5 {
		t.Fatalf("incomplete round: %+v calls=%d", status.Runs, p.calls.Load())
	}
	if len(b.inputs) != 4 {
		t.Fatal(len(b.inputs))
	}
	for _, report := range b.inputs[2].Prior {
		if report.Stage == 2 {
			t.Fatal("critic saw risk")
		}
	}
	if len(b.inputs[3].Prior) != 3 {
		t.Fatal("Atlas lacks reports", b.inputs[3].Prior)
	}
	strategies, err := s.Strategies(context.Background())
	if err != nil || len(strategies) != 1 {
		t.Fatal(len(strategies), err)
	}
	var artifact map[string]any
	json.Unmarshal(strategies[0], &artifact)
	if artifact["schema_version"] != "strategy.v1" || artifact["version"] != float64(1) || artifact["status"] != "research" {
		t.Fatal(artifact)
	}
	var events int
	store.DB.QueryRow(context.Background(), `SELECT count(*) FROM conversation_events`).Scan(&events)
	if events != 0 {
		t.Fatal("forged owner conversation")
	}
}

func TestSchedulerCadenceNoCatchupAndMaterialChange(t *testing.T) {
	s, _, _, b := fixture(t)
	at := time.Now().UTC().Truncate(time.Microsecond)
	tick(t, s, at)
	for i := 0; i < 5; i++ {
		process(t, s)
	}
	tick(t, s, at.Add(30*time.Minute))
	st, _ := s.Status(context.Background())
	if len(st.Runs) != 1 {
		t.Fatal("early run")
	}
	b.fingerprint = "changed"
	tick(t, s, at.Add(59*time.Minute))
	st, _ = s.Status(context.Background())
	if len(st.Runs) != 1 {
		t.Fatal("material change ignored one-hour floor")
	}
	tick(t, s, at.Add(time.Hour))
	st, _ = s.Status(context.Background())
	if len(st.Runs) != 2 || st.NextRunAt.Sub(at.Add(time.Hour)) != 6*time.Hour {
		t.Fatal(st)
	}
	for i := 0; i < 5; i++ {
		process(t, s)
	}
	tick(t, s, at.Add(48*time.Hour))
	st, _ = s.Status(context.Background())
	if len(st.Runs) != 3 {
		t.Fatal("catchup avalanche", len(st.Runs))
	}
}

func TestPauseAndManualPriorityPreventInference(t *testing.T) {
	s, store, p, _ := fixture(t)
	tick(t, s, time.Now())
	if err := s.SetPaused(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ProcessOne(context.Background()); ok || err != nil {
		t.Fatal(ok, err)
	}
	s.SetPaused(context.Background(), false)
	_, err := store.DB.Exec(context.Background(), `WITH e AS(INSERT INTO conversation_events(user_id,channel,external_key,direction,body) VALUES($1,'dashboard','priority','incoming','Teste') RETURNING id) INSERT INTO council_jobs(event_id,case_id,agent_id) SELECT id,$2,'A01' FROM e`, memory.UserID, memory.ID())
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ProcessOne(context.Background()); ok || err != nil || p.calls.Load() != 0 {
		t.Fatal(ok, err, p.calls.Load())
	}
}
