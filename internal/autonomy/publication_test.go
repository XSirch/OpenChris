package autonomy

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"openchris/conselho/internal/codex"
	"openchris/conselho/internal/research"
)

func TestConsentExpiresWhileFinalPublicationWriteWaits(t *testing.T) {
	s, store, _, b := fixture(t)
	ctx := context.Background()
	tick(t, s, time.Now().UTC())
	for i := 0; i < 4; i++ {
		process(t, s)
	}
	s.now = func() time.Time { return time.Now().UTC() }
	// This local trigger blocks the artifact write after the first fingerprint
	// check. Cleanup is guarded by fixture's exact isolated database assertion.
	_, err := store.DB.Exec(ctx, `CREATE OR REPLACE FUNCTION autonomy_test_publication_wait() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(2026110703); RETURN NEW; END $$; CREATE TRIGGER autonomy_test_wait BEFORE INSERT ON autonomy_strategy_versions FOR EACH ROW EXECUTE FUNCTION autonomy_test_publication_wait()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if store.DB.Config().ConnConfig.Database != "conselho_autonomy_test" {
			t.Error("refusing trigger cleanup")
			return
		}
		_, err := store.DB.Exec(ctx, `DROP TRIGGER IF EXISTS autonomy_test_wait ON autonomy_strategy_versions; DROP FUNCTION IF EXISTS autonomy_test_publication_wait()`)
		if err != nil {
			t.Error(err)
		}
	})
	blocker, err := store.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err = blocker.Exec(ctx, `SELECT pg_advisory_xact_lock(2026110703)`); err != nil {
		t.Fatal(err)
	}
	b.validUntil = time.Now().UTC().Add(500 * time.Millisecond)
	done := make(chan error, 1)
	go func() { _, err := s.ProcessOne(ctx); done <- err }()
	deadline := time.Now().Add(3 * time.Second)
	waiting := false
	for time.Now().Before(deadline) {
		if err = store.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=2026110703 AND NOT granted)`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !waiting {
		t.Fatal("publication did not reach blocked insert")
	}
	time.Sleep(time.Until(b.validUntil) + 20*time.Millisecond)
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	strategies, err := s.Strategies(ctx)
	if err != nil || len(strategies) != 0 {
		t.Fatal("expired artifact committed", len(strategies), err)
	}
	detail, err := s.RunDetail(ctx, mustRun(t, s))
	if err != nil || detail.Run.Status != "stale" || len(detail.Tasks) != 5 || detail.Tasks[4].Status != "stale" || (len(detail.Tasks[4].Result) > 0 && string(detail.Tasks[4].Result) != "null") {
		t.Fatal(detail, err)
	}
}

type verifyingCollector struct {
	fakeCollector
	verifyCalls int
	doc         research.Document
}

func (c *verifyingCollector) VerifyDocument(context.Context, string) (research.Document, error) {
	c.verifyCalls++
	return c.doc, nil
}
func TestResearchUsesServerVerifiedDocumentAndIDs(t *testing.T) {
	s, _, p, b := fixture(t)
	at := time.Now().UTC()
	published := at.Add(-time.Hour)
	c := &verifyingCollector{doc: research.Document{URL: "https://www.bcb.gov.br/verified", Title: "Título observado pelo servidor", Summary: "Conteúdo observado", Kind: "primary", Hash: "verified", PublishedAt: &published}}
	s.collector = c
	p.research = func(_ context.Context, in codex.ResearchRequest) (codex.ResearchResult, error) {
		if in.Topic != "fixed_income" || in.AsOf.IsZero() {
			t.Fatal(in)
		}
		raw, _ := json.Marshal(in)
		var fields map[string]any
		json.Unmarshal(raw, &fields)
		if len(fields) != 3 {
			t.Fatal("public research includes extra context", fields)
		}
		return codex.ResearchResult{Summary: "Pesquisa", Sources: []codex.ResearchSource{{URL: "https://www.bcb.gov.br/claimed", Title: "Inventado pelo modelo", Summary: "Não observado"}}, Unknowns: []string{}, Model: codex.DefaultModel}, nil
	}
	tick(t, s, at)
	process(t, s)
	process(t, s)
	if c.verifyCalls != 1 || len(b.inputs) != 1 || len(b.inputs[0].Evidence) != 1 {
		t.Fatal(c.verifyCalls, b.inputs)
	}
	e := b.inputs[0].Evidence[0]
	if e.ID == "" || e.Title != c.doc.Title || e.URL != c.doc.URL || e.Summary != c.doc.Summary {
		t.Fatal("model supplied evidence was trusted", e)
	}
}

func TestNoRiskProfileCannotBecomeCompleteFromEmptyModelUnknowns(t *testing.T) {
	s, _, p, _ := fixture(t)
	at := time.Now().UTC()
	published := at.Add(-time.Hour)
	s.collector = &fakeCollector{docs: []research.Document{{URL: "https://www.bcb.gov.br/fresh", Title: "Nota", Summary: "Notícia recente", Kind: "primary", Hash: "fresh", PublishedAt: &published}}}
	p.run = func(_ context.Context, in codex.Request) (codex.Result, error) {
		out := answer(in)
		out.Unknowns = []string{}
		return out, nil
	}
	tick(t, s, at)
	for i := 0; i < 5; i++ {
		process(t, s)
	}
	st, _ := s.Status(context.Background())
	if st.Runs[0].Status != "await_data" {
		t.Fatal("model silence became known risk profile", st.Runs[0])
	}
}

func TestEvidenceKeepsActualObservationTime(t *testing.T) {
	s, _, _, _ := fixture(t)
	at := time.Now().UTC().Truncate(time.Microsecond)
	observed := at.Add(-time.Minute)
	s.collector = &fakeCollector{docs: []research.Document{{URL: "https://www.bcb.gov.br/observed", Title: "Nota observada", Summary: "Documento público", Kind: "primary", Hash: "observed", CollectedAt: observed}}}
	tick(t, s, at)
	detail, err := s.RunDetail(context.Background(), mustRun(t, s))
	if err != nil || len(detail.Evidence) != 1 || !detail.Evidence[0].ObservedAt.Equal(observed) {
		t.Fatal("observation timestamp replaced by scheduler", detail.Evidence, err)
	}
}
