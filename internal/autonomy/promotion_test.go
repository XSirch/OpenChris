package autonomy

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"openchris/conselho/internal/codex"
	"openchris/conselho/internal/research"
)

func feedAndDocument(t *testing.T) (*Service, *verifyingCollector, *fakeProvider, time.Time) {
	t.Helper()
	s, _, p, _ := fixture(t)
	cutoff := time.Now().UTC().Truncate(time.Microsecond)
	published := cutoff.Add(-time.Hour)
	feed := research.Document{URL: "https://www.bcb.gov.br/promoted", Title: "Comunicado", Summary: "Resumo do feed", Kind: "primary", Hash: "rss-version", PublishedAt: &published, CollectedAt: cutoff}
	doc := feed
	doc.Summary = "Conteúdo completo observado na página"
	doc.Hash = "document-version"
	doc.CollectedAt = cutoff.Add(20 * time.Second)
	c := &verifyingCollector{fakeCollector: fakeCollector{docs: []research.Document{feed}}, doc: doc}
	s.collector = c
	p.research = func(context.Context, codex.ResearchRequest) (codex.ResearchResult, error) {
		s.now = func() time.Time { return doc.CollectedAt }
		return codex.ResearchResult{Summary: "Pesquisa pública", Sources: []codex.ResearchSource{{URL: doc.URL}}, Unknowns: []string{}, Model: codex.DefaultModel}, nil
	}
	tick(t, s, cutoff)
	return s, c, p, cutoff
}

func TestResearchPromotesSelectedFeedToVerifiedDocumentAndCompletesRound(t *testing.T) {
	s, c, p, cutoff := feedAndDocument(t)
	ctx := context.Background()
	runID := mustRun(t, s)
	before, err := s.RunDetail(ctx, runID)
	if err != nil || len(before.Evidence) != 1 {
		t.Fatal(before, err)
	}
	feedID := before.Evidence[0].ID
	process(t, s)
	detail, err := s.RunDetail(ctx, runID)
	if err != nil || detail.Tasks[0].Status != "complete" || len(detail.Evidence) != 1 || detail.Evidence[0].ID == feedID || detail.Evidence[0].Summary != c.doc.Summary || !detail.Evidence[0].ObservedAt.Equal(c.doc.CollectedAt) {
		t.Fatal("research invalidated its own verified promotion", detail, err)
	}
	var versions int
	if err = s.db.QueryRow(ctx, `SELECT count(*) FROM autonomy_evidence WHERE url=$1`, c.doc.URL).Scan(&versions); err != nil || versions != 2 {
		t.Fatal("promotion did not preserve immutable versions", versions, err)
	}
	// A conditional collection may repeat the unchanged RSS content. Its older
	// immutable version must not invalidate the verified page already selected.
	tick(t, s, cutoff.Add(15*time.Minute))
	for i := 0; i < 4; i++ {
		process(t, s)
	}
	detail, err = s.RunDetail(ctx, runID)
	if err != nil || detail.Run.Status != "await_data" || p.calls.Load() != 5 || len(detail.Evidence) != 1 {
		t.Fatal("promoted research could not finish five stages", detail, p.calls.Load(), err)
	}
	for _, task := range detail.Tasks {
		if task.Status != "complete" {
			t.Fatal("mandatory stage missing after promotion", task)
		}
	}
}

func TestExternalFeedCorrectionDuringResearchCannotBeHiddenByPromotion(t *testing.T) {
	s, c, p, cutoff := feedAndDocument(t)
	ctx := context.Background()
	runID := mustRun(t, s)
	p.research = func(ctx context.Context, _ codex.ResearchRequest) (codex.ResearchResult, error) {
		correction := c.docs[0]
		correction.Hash = "external-rss-correction"
		correction.Summary = "Correção externa durante a inferência"
		correction.CollectedAt = cutoff.Add(10 * time.Second)
		tx, err := s.db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err = saveDocument(ctx, tx, research.DefaultFeeds()[0].ID, correction, correction.CollectedAt); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		s.now = func() time.Time { return c.doc.CollectedAt }
		return codex.ResearchResult{Summary: "Pesquisa", Sources: []codex.ResearchSource{{URL: c.doc.URL}}, Unknowns: []string{}, Model: codex.DefaultModel}, nil
	}
	process(t, s)
	detail, err := s.RunDetail(ctx, runID)
	if err != nil || detail.Run.Status != "stale" || detail.Tasks[0].Status != "stale" || len(detail.Evidence) != 1 || detail.Evidence[0].Summary != c.docs[0].Summary {
		t.Fatal("external correction was hidden by own promotion", detail, err)
	}
	var promoted int
	if err = s.db.QueryRow(ctx, `SELECT count(*) FROM autonomy_evidence WHERE content_hash=$1`, c.doc.Hash).Scan(&promoted); err != nil || promoted != 0 {
		t.Fatal("stale research committed a document", promoted, err)
	}
	if ok, err := s.ProcessOne(ctx); ok || err != nil || p.calls.Load() != 1 {
		t.Fatal("stale research chained another inference", ok, err, p.calls.Load())
	}
}

func TestChangedFeedAfterPromotionInvalidatesNextPrivateStage(t *testing.T) {
	s, c, _, cutoff := feedAndDocument(t)
	process(t, s)
	process(t, s)
	c.docs[0].Hash = "later-rss-correction"
	c.docs[0].Summary = "Correção posterior à consulta completa"
	c.docs[0].CollectedAt = cutoff.Add(15 * time.Minute)
	tick(t, s, cutoff.Add(15*time.Minute))
	process(t, s)
	detail, err := s.RunDetail(context.Background(), mustRun(t, s))
	if err != nil || detail.Run.Status != "stale" || detail.Tasks[2].Result != nil {
		t.Fatal("changed external feed did not invalidate promoted dossier", detail, err)
	}
}

type expireOnPromotedDocument struct {
	Builder
	sawPromoted bool
}

func (b *expireOnPromotedDocument) Fingerprint(ctx context.Context, tx pgx.Tx, at time.Time) (string, error) {
	var promoted bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM autonomy_run_evidence r JOIN autonomy_evidence e ON e.id=r.evidence_id WHERE e.content_hash='document-version')`).Scan(&promoted); err != nil {
		return "", err
	}
	if promoted {
		b.sawPromoted = true
		return "expired-after-promotion", nil
	}
	return b.Builder.Fingerprint(ctx, tx, at)
}

func TestExpiredConsentRollsBackDocumentAndSelectionPromotion(t *testing.T) {
	s, c, p, _ := feedAndDocument(t)
	ctx := context.Background()
	runID := mustRun(t, s)
	before, err := s.RunDetail(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	b := &expireOnPromotedDocument{Builder: s.builder}
	s.builder = b
	process(t, s)
	detail, err := s.RunDetail(ctx, runID)
	if err != nil || !b.sawPromoted || detail.Run.Status != "stale" || detail.Tasks[0].Status != "stale" || len(detail.Evidence) != 1 || detail.Evidence[0].ID != before.Evidence[0].ID || !detail.Evidence[0].ObservedAt.Equal(before.Evidence[0].ObservedAt) {
		t.Fatal("promotion was not rolled back atomically", detail, b.sawPromoted, err)
	}
	var documents, observations int
	if err = s.db.QueryRow(ctx, `SELECT count(*) FROM autonomy_evidence WHERE content_hash=$1`, c.doc.Hash).Scan(&documents); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(ctx, `SELECT count(*) FROM autonomy_evidence_observations o JOIN autonomy_sources s ON s.id=o.source_id WHERE NOT s.is_feed`).Scan(&observations); err != nil {
		t.Fatal(err)
	}
	if documents != 0 || observations != 0 || p.calls.Load() != 1 {
		t.Fatal("rolled-back promotion left content or observation", documents, observations, p.calls.Load())
	}
}

func TestZeroCollectedAtUsesOneServerObservationInstant(t *testing.T) {
	s, _, p, _ := fixture(t)
	cutoff := time.Now().UTC().Truncate(time.Microsecond)
	published := cutoff.Add(-time.Hour)
	c := &verifyingCollector{doc: research.Document{URL: "https://www.bcb.gov.br/zero-observed", Title: "Nota", Summary: "Observação pelo servidor", Hash: "zero-observed", Kind: "primary", PublishedAt: &published}}
	s.collector = c
	p.research = func(context.Context, codex.ResearchRequest) (codex.ResearchResult, error) {
		return codex.ResearchResult{Summary: "Pesquisa", Sources: []codex.ResearchSource{{URL: c.doc.URL}}, Unknowns: []string{}, Model: codex.DefaultModel}, nil
	}
	tick(t, s, cutoff)
	now := cutoff.Add(time.Second)
	s.now = func() time.Time { now = now.Add(time.Microsecond); return now }
	process(t, s)
	detail, err := s.RunDetail(context.Background(), mustRun(t, s))
	if err != nil || detail.Tasks[0].Status != "complete" || len(detail.Evidence) != 1 || !detail.Evidence[0].ObservedAt.After(cutoff) {
		t.Fatal("server observation timestamp did not match saved observation", detail, err)
	}
}
