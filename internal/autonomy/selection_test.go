package autonomy

import (
	"context"
	"fmt"
	"testing"
	"time"

	"openchris/conselho/internal/research"
)

type catalogCollector struct {
	fakeCollector
	documents map[string][]research.Document
}

func (c *catalogCollector) FetchFeed(_ context.Context, source research.Source, _, _ string) (research.FeedResult, error) {
	return research.FeedResult{Documents: c.documents[source.ID], ETag: "fixture"}, nil
}

func TestInitialTopTwelvePrioritizesPublicationOverCollectionOrder(t *testing.T) {
	s, _, _, builder := fixture(t)
	ctx := context.Background()
	cutoff := time.Now().UTC().Truncate(time.Microsecond)
	c := &catalogCollector{documents: map[string][]research.Document{}}
	for i := 0; i < 8; i++ {
		published := cutoff.Add(-time.Duration(i+1) * time.Hour)
		doc := research.Document{URL: fmt.Sprintf("https://agenciabrasil.ebc.com.br/economia/fixture-%d", i), Title: "Notícia econômica", Summary: "Conteúdo atual", Kind: "news", Hash: fmt.Sprintf("news-%d", i), PublishedAt: &published, CollectedAt: cutoff.Add(-2 * time.Minute)}
		if i == 0 {
			// The previous content version claimed a newer publication time.
			// Latest-by-observation must still choose the corrected version.
			olderPublication := cutoff.Add(-5 * time.Minute)
			older := doc
			older.Hash, older.Summary, older.PublishedAt, older.CollectedAt = "superseded-news", "Versão anterior", &olderPublication, cutoff.Add(-3*time.Minute)
			c.documents["agencia-brasil-economia"] = append(c.documents["agencia-brasil-economia"], older)
		}
		c.documents["agencia-brasil-economia"] = append(c.documents["agencia-brasil-economia"], doc)
	}
	for i := 0; i < 14; i++ {
		published := cutoff.Add(-time.Duration(60+i) * 24 * time.Hour)
		source := "cvm-decisoes"
		if i >= 7 {
			source = "cvm-legislacao"
		}
		c.documents[source] = append(c.documents[source], research.Document{URL: fmt.Sprintf("https://conteudo.cvm.gov.br/fixture-%d", i), Title: "Documento anterior da CVM", Summary: "Publicação antiga coletada agora", Kind: "primary", Hash: fmt.Sprintf("cvm-%d", i), PublishedAt: &published, CollectedAt: cutoff.Add(-time.Minute).Add(time.Duration(i) * time.Microsecond)})
	}
	c.documents["cvm-legislacao"] = append(c.documents["cvm-legislacao"], research.Document{URL: "https://conteudo.cvm.gov.br/unknown-date", Title: "Documento sem data", Summary: "Data de publicação desconhecida", Kind: "primary", Hash: "unknown-date", CollectedAt: cutoff.Add(-time.Second)})
	s.collector = c
	tick(t, s, cutoff)
	detail, err := s.RunDetail(ctx, mustRun(t, s))
	if err != nil || len(detail.Evidence) != 12 {
		t.Fatal("wrong selection size", len(detail.Evidence), err)
	}
	news, primary := 0, 0
	for _, evidence := range detail.Evidence {
		if evidence.PublishedAt == nil {
			t.Fatal("unknown date displaced a dated publication", evidence)
		}
		if evidence.Kind == "news" {
			news++
			if evidence.Summary != "Conteúdo atual" || !evidence.ObservedAt.Equal(cutoff.Add(-2*time.Minute)) {
				t.Fatal("publication ranking changed latest-by-URL version or observation time", evidence)
			}
		} else {
			primary++
			if evidence.PublishedAt.After(cutoff.Add(-60 * 24 * time.Hour)) {
				t.Fatal("recent collection freshened an old publication", evidence)
			}
		}
	}
	if news != 8 || primary != 4 {
		t.Fatal("later CVM collection displaced current economic news", news, primary)
	}
	assertConsumerPriority := func(evidence []Evidence) {
		t.Helper()
		if len(evidence) != 12 {
			t.Fatal("consumer lost selected evidence", len(evidence))
		}
		for i := 0; i < 8; i++ {
			if evidence[i].Kind != "news" || evidence[i].PublishedAt == nil {
				t.Fatal("consumer cap would displace recent news with older CVM documents", i, evidence[i])
			}
			expected := cutoff.Add(-time.Duration(i+1) * time.Hour)
			if !evidence[i].PublishedAt.Equal(expected) {
				t.Fatal("consumer reordered news by collection instead of publication", i, evidence[i].PublishedAt, expected)
			}
		}
	}
	assertConsumerPriority(detail.Evidence)
	var documents, unknownDates int
	if err = s.db.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE published_at IS NULL) FROM autonomy_evidence`).Scan(&documents, &unknownDates); err != nil || documents != 24 || unknownDates != 1 {
		t.Fatal("selection discarded unknown dates or immutable versions from history", documents, unknownDates, err)
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
	before := readFingerprint(cutoff)
	tick(t, s, cutoff.Add(15*time.Minute))
	if after := readFingerprint(cutoff.Add(15 * time.Minute)); after != before {
		t.Fatal("selection ranking or unchanged collection changed material content fingerprint", before, after)
	}
	process(t, s)
	process(t, s)
	if len(builder.inputs) != 1 {
		t.Fatal("strategy context was not built", len(builder.inputs))
	}
	assertConsumerPriority(builder.inputs[0].Evidence)
}
