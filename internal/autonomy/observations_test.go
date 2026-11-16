package autonomy

import (
	"context"
	"testing"
	"time"

	"openchris/conselho/internal/codex"
	"openchris/conselho/internal/research"
)

func TestFeedReversionSelectsLatestObservationWithoutChangingHistory(t *testing.T) {
	s, _, _, _ := fixture(t)
	ctx := context.Background()
	cutoff := time.Now().UTC().Truncate(time.Microsecond)
	published := cutoff.Add(-time.Hour)
	original := research.Document{URL: "https://www.bcb.gov.br/reverted", Title: "Nota", Summary: "Conteúdo A", Kind: "primary", Hash: "content-a", PublishedAt: &published, CollectedAt: cutoff}
	c := &fakeCollector{docs: []research.Document{original}}
	s.collector = c
	tick(t, s, cutoff)
	oldID := mustRun(t, s)
	old, err := s.RunDetail(ctx, oldID)
	if err != nil || len(old.Evidence) != 1 {
		t.Fatal(old, err)
	}
	for i := 0; i < 5; i++ {
		process(t, s)
	}
	c.docs[0].Summary, c.docs[0].Hash, c.docs[0].CollectedAt = "Conteúdo B", "content-b", cutoff.Add(15*time.Minute)
	tick(t, s, cutoff.Add(15*time.Minute))
	c.docs[0] = original
	c.docs[0].CollectedAt = cutoff.Add(30 * time.Minute)
	tick(t, s, cutoff.Add(30*time.Minute))
	if _, err = s.RequestRun(ctx); err != nil {
		t.Fatal(err)
	}
	latest, err := s.RunDetail(ctx, mustRun(t, s))
	if err != nil || len(latest.Evidence) != 1 || latest.Evidence[0].ID != old.Evidence[0].ID || latest.Evidence[0].Summary != "Conteúdo A" || !latest.Evidence[0].ObservedAt.Equal(c.docs[0].CollectedAt) {
		t.Fatal("A-B-A reversion retained B or lost its current observation", latest.Evidence, err)
	}
	oldAgain, err := s.RunDetail(ctx, oldID)
	if err != nil || len(oldAgain.Evidence) != 1 || !oldAgain.Evidence[0].ObservedAt.Equal(old.Evidence[0].ObservedAt) || oldAgain.Evidence[0].Summary != old.Evidence[0].Summary {
		t.Fatal("re-observation rewrote the historical dossier", oldAgain.Evidence, err)
	}
	var count int
	if err = s.db.QueryRow(ctx, `SELECT count(*) FROM autonomy_evidence WHERE url=$1`, original.URL).Scan(&count); err != nil || count != 2 {
		t.Fatal("re-observation duplicated immutable content", count, err)
	}
}

func TestReverificationOfSameDocumentAfterChangedFeedCompletesNewRound(t *testing.T) {
	s, c, p, cutoff := feedAndDocument(t)
	for i := 0; i < 5; i++ {
		process(t, s)
	}
	c.docs[0].Hash, c.docs[0].Summary, c.docs[0].CollectedAt = "changed-rss", "Novo resumo RSS", cutoff.Add(30*time.Minute)
	tick(t, s, cutoff.Add(30*time.Minute))
	tick(t, s, cutoff.Add(time.Hour))
	if _, err := s.RequestRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.doc.CollectedAt = cutoff.Add(time.Hour + 20*time.Second)
	p.research = func(context.Context, codex.ResearchRequest) (codex.ResearchResult, error) {
		s.now = func() time.Time { return c.doc.CollectedAt }
		return codex.ResearchResult{Summary: "Reconsulta da página", Sources: []codex.ResearchSource{{URL: c.doc.URL}}, Unknowns: []string{}, Model: codex.DefaultModel}, nil
	}
	for i := 0; i < 5; i++ {
		process(t, s)
	}
	detail, err := s.RunDetail(context.Background(), mustRun(t, s))
	if err != nil || detail.Run.Status != "await_data" || len(detail.Evidence) != 1 || !detail.Evidence[0].ObservedAt.Equal(c.doc.CollectedAt) || detail.Evidence[0].Summary != c.doc.Summary {
		t.Fatal("same HTML content reused an older observation than the RSS correction", detail, err)
	}
	for _, task := range detail.Tasks {
		if task.Status != "complete" {
			t.Fatal(task)
		}
	}
}

func TestIntermediateCorrectionWithSameTimestampInvalidatesResearch(t *testing.T) {
	s, c, p, cutoff := feedAndDocument(t)
	p.research = func(ctx context.Context, _ codex.ResearchRequest) (codex.ResearchResult, error) {
		tx, err := s.db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		correction := c.docs[0]
		correction.Hash, correction.Summary = "intermediate-b", "Correção intermediária B"
		if _, err = saveDocument(ctx, tx, research.DefaultFeeds()[0].ID, correction, cutoff); err != nil {
			t.Fatal(err)
		}
		if _, err = saveDocument(ctx, tx, research.DefaultFeeds()[0].ID, c.docs[0], cutoff); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return codex.ResearchResult{Summary: "Pesquisa", Sources: []codex.ResearchSource{}, Unknowns: []string{}, Model: codex.DefaultModel}, nil
	}
	process(t, s)
	detail, err := s.RunDetail(context.Background(), mustRun(t, s))
	if err != nil || detail.Run.Status != "stale" || detail.Tasks[0].Status != "stale" {
		t.Fatal("A-B-A within one timestamp hid a correction during inference", detail, err)
	}
	var latest string
	if err = s.db.QueryRow(context.Background(), `SELECT e.content_hash FROM autonomy_evidence_observations o JOIN autonomy_evidence e ON e.id=o.evidence_id WHERE o.url=$1 ORDER BY o.observed_at DESC,o.id DESC LIMIT 1`, c.doc.URL).Scan(&latest); err != nil || latest != c.docs[0].Hash {
		t.Fatal("same-timestamp A-B-A lost the final observation A", latest, err)
	}
}

func TestIdenticalPageReverificationDoesNotChangeMaterialFingerprint(t *testing.T) {
	s, c, p, cutoff := feedAndDocument(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		process(t, s)
	}
	readFingerprint := func(at time.Time) string {
		tx, err := s.db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		fp, err := evidenceFingerprint(ctx, tx, at)
		if err != nil {
			t.Fatal(err)
		}
		return fp
	}
	before := readFingerprint(c.doc.CollectedAt)
	now := cutoff.Add(6 * time.Hour)
	s.now = func() time.Time { return now }
	if _, err := s.RequestRun(ctx); err != nil {
		t.Fatal(err)
	}
	c.doc.CollectedAt = now.Add(20 * time.Second)
	p.research = func(context.Context, codex.ResearchRequest) (codex.ResearchResult, error) {
		s.now = func() time.Time { return c.doc.CollectedAt }
		return codex.ResearchResult{Summary: "Reconsulta", Sources: []codex.ResearchSource{{URL: c.doc.URL}}, Unknowns: []string{}, Model: codex.DefaultModel}, nil
	}
	process(t, s)
	if after := readFingerprint(c.doc.CollectedAt); after != before {
		t.Fatal("an identical page observation became a material change", before, after)
	}
}

type notModifiedCollector struct {
	fakeCollector
	notModified      bool
	conditionalCalls int
}

func (c *notModifiedCollector) FetchFeed(ctx context.Context, source research.Source, etag, modified string) (research.FeedResult, error) {
	if etag == "fixture" {
		c.conditionalCalls++
	}
	if c.notModified {
		return research.FeedResult{NotModified: true, ETag: etag, LastModified: modified}, nil
	}
	return c.fakeCollector.FetchFeed(ctx, source, etag, modified)
}

func TestUnchangedFeedAnd304DoNotAddObservations(t *testing.T) {
	s, _, _, _ := fixture(t)
	ctx := context.Background()
	cutoff := time.Now().UTC().Truncate(time.Microsecond)
	c := &notModifiedCollector{fakeCollector: fakeCollector{docs: []research.Document{{URL: "https://www.bcb.gov.br/unchanged", Title: "Nota", Summary: "Conteúdo igual", Kind: "primary", Hash: "unchanged", CollectedAt: cutoff}}}}
	s.collector = c
	tick(t, s, cutoff)
	count := func() int {
		var total int
		if err := s.db.QueryRow(ctx, `SELECT count(*) FROM autonomy_evidence_observations`).Scan(&total); err != nil {
			t.Fatal(err)
		}
		return total
	}
	before := count()
	if before != len(research.DefaultFeeds()) {
		t.Fatal("initial observations missing", before)
	}
	c.docs[0].CollectedAt = cutoff.Add(15 * time.Minute)
	tick(t, s, cutoff.Add(15*time.Minute))
	if after := count(); after != before {
		t.Fatal("unchanged feed duplicated observations", before, after)
	}
	c.notModified = true
	tick(t, s, cutoff.Add(30*time.Minute))
	if after := count(); after != before || c.conditionalCalls != 2*len(research.DefaultFeeds()) {
		t.Fatal("304 or conditional fetching lost dedupe", after, c.conditionalCalls)
	}
}

func TestSuccessfulPageReverificationRefreshesSourceHealth(t *testing.T) {
	s, c, p, cutoff := feedAndDocument(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		process(t, s)
	}
	var beforeChecked, beforeSuccess time.Time
	var sourceID, sourceURL string
	if err := s.db.QueryRow(ctx, `SELECT id,url,last_checked_at,last_success_at FROM autonomy_sources WHERE NOT is_feed`).Scan(&sourceID, &sourceURL, &beforeChecked, &beforeSuccess); err != nil {
		t.Fatal(err)
	}
	now := cutoff.Add(6 * time.Hour)
	s.now = func() time.Time { return now }
	if _, err := s.RequestRun(ctx); err != nil {
		t.Fatal(err)
	}
	c.doc.CollectedAt = now.Add(20 * time.Second)
	p.research = func(context.Context, codex.ResearchRequest) (codex.ResearchResult, error) {
		s.now = func() time.Time { return c.doc.CollectedAt }
		return codex.ResearchResult{Summary: "Reconsulta", Sources: []codex.ResearchSource{{URL: c.doc.URL}}, Unknowns: []string{}, Model: codex.DefaultModel}, nil
	}
	process(t, s)
	var checked, success time.Time
	var id, url, state string
	if err := s.db.QueryRow(ctx, `SELECT id,url,state,last_checked_at,last_success_at FROM autonomy_sources WHERE NOT is_feed`).Scan(&id, &url, &state, &checked, &success); err != nil {
		t.Fatal(err)
	}
	if id != sourceID || url != sourceURL || state != "ok" || !checked.Equal(c.doc.CollectedAt) || !success.Equal(c.doc.CollectedAt) || !checked.After(beforeChecked) || !success.After(beforeSuccess) {
		t.Fatal("successful re-verification left source health stale or changed identity", id, url, state, checked, success)
	}
}
