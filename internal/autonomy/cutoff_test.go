package autonomy

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"openchris/conselho/internal/codex"
	"openchris/conselho/internal/research"
)

func TestVerifiedPublicationAfterCutoffStaysOutOfRound(t *testing.T) {
	s, store, p, b := fixture(t)
	cutoff := time.Now().UTC().Truncate(time.Microsecond)
	published := cutoff.Add(time.Minute)
	observed := cutoff.Add(2 * time.Minute)
	c := &verifyingCollector{doc: research.Document{URL: "https://www.bcb.gov.br/after-cutoff", Title: "Nota posterior", Summary: "Publicada após o corte", Kind: "primary", Hash: "after-cutoff", PublishedAt: &published, CollectedAt: observed}}
	s.collector = c
	p.research = func(context.Context, codex.ResearchRequest) (codex.ResearchResult, error) {
		s.now = func() time.Time { return observed }
		return codex.ResearchResult{Summary: "Pesquisa", Sources: []codex.ResearchSource{{URL: c.doc.URL, Title: "Data desconhecida para o modelo", PublishedAt: nil}}, Unknowns: []string{}, Model: codex.DefaultModel}, nil
	}
	tick(t, s, cutoff)
	process(t, s)
	process(t, s)
	if c.verifyCalls != 1 || len(b.inputs) != 1 || len(b.inputs[0].Evidence) != 0 {
		t.Fatal("verified future publication entered private context", b.inputs)
	}
	var raw []byte
	if err := store.DB.QueryRow(context.Background(), `SELECT result FROM autonomy_tasks WHERE stage=0`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var result codex.ResearchResult
	if err := json.Unmarshal(raw, &result); err != nil || len(result.Sources) != 0 || len(result.Unknowns) == 0 {
		t.Fatal("future reference was not recorded as a gap", result, err)
	}
}

func TestInitialSelectionExcludesFuturePublicationUntilEligible(t *testing.T) {
	s, _, _, _ := fixture(t)
	cutoff := time.Now().UTC().Truncate(time.Microsecond)
	published := cutoff.Add(2 * time.Hour)
	s.collector = &fakeCollector{docs: []research.Document{{URL: "https://www.bcb.gov.br/future-feed", Title: "Nota futura", Summary: "Ainda não publicada no corte", Kind: "primary", Hash: "future-feed", PublishedAt: &published}}}
	tick(t, s, cutoff)
	detail, err := s.RunDetail(context.Background(), mustRun(t, s))
	if err != nil || len(detail.Evidence) != 0 {
		t.Fatal("initial selection includes future publication", detail.Evidence, err)
	}
	for i := 0; i < 5; i++ {
		process(t, s)
	}
	tick(t, s, cutoff.Add(time.Hour))
	st, err := s.Status(context.Background())
	if err != nil || len(st.Runs) != 1 {
		t.Fatal("future evidence changed material fingerprint early", st.Runs, err)
	}
	tick(t, s, published)
	st, err = s.Status(context.Background())
	if err != nil || len(st.Runs) != 1 {
		t.Fatal("public publication triggered a financial change", st.Runs, err)
	}
	if _, err = s.RequestRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	detail, err = s.RunDetail(context.Background(), mustRun(t, s))
	if err != nil || len(detail.Evidence) != 1 {
		t.Fatal("published evidence missing from next requested round", detail.Evidence, err)
	}
}

func TestRoundFreshnessUsesCutoffAndPreservesLaterObservation(t *testing.T) {
	s, _, p, b := fixture(t)
	cutoff := time.Now().UTC().Truncate(time.Microsecond)
	published := cutoff.Add(-7*24*time.Hour + time.Hour)
	observed := cutoff.Add(2 * time.Minute)
	s.collector = &verifyingCollector{doc: research.Document{URL: "https://www.bcb.gov.br/known-at-cutoff", Title: "Nota anterior", Summary: "Publicação anterior ao corte", Kind: "primary", Hash: "known-at-cutoff", PublishedAt: &published, CollectedAt: observed}}
	p.research = func(context.Context, codex.ResearchRequest) (codex.ResearchResult, error) {
		s.now = func() time.Time { return observed }
		return codex.ResearchResult{Summary: "Pesquisa", Sources: []codex.ResearchSource{{URL: "https://www.bcb.gov.br/known-at-cutoff"}}, Unknowns: []string{}, Model: codex.DefaultModel}, nil
	}
	p.run = func(_ context.Context, in codex.Request) (codex.Result, error) {
		out := answer(in)
		out.Unknowns = []string{}
		return out, nil
	}
	tick(t, s, cutoff)
	for i := 0; i < 4; i++ {
		process(t, s)
	}
	s.now = func() time.Time { return cutoff.Add(2 * time.Hour) }
	process(t, s)
	st, err := s.Status(context.Background())
	if err != nil || st.Runs[0].ErrorCode != "personal_data_missing" {
		t.Fatal("age was recomputed against completion time", st.Runs, err)
	}
	if len(b.inputs) != 4 || len(b.inputs[0].Evidence) != 1 || !b.inputs[0].Evidence[0].ObservedAt.Equal(observed) {
		t.Fatal("later observation time was rewritten", b.inputs)
	}
}

func TestStrategyMemoryRecoversPriorCritiqueWithoutChangingArtifactSchema(t *testing.T) {
	s, _, p, b := fixture(t)
	cutoff := time.Now().UTC()
	p.run = func(_ context.Context, in codex.Request) (codex.Result, error) {
		out := answer(in)
		if in.AgentID == "A21" {
			out.Reply = "A tese depende de um prazo de resgate que não foi comprovado."
			out.Unknowns = []string{"Confirmar prazo de resgate"}
		}
		return out, nil
	}
	tick(t, s, cutoff)
	for i := 0; i < 5; i++ {
		process(t, s)
	}
	tick(t, s, cutoff.Add(6*time.Hour))
	process(t, s)
	process(t, s)
	if len(b.inputs) != 5 || len(b.inputs[4].Strategies) != 1 {
		t.Fatal("strategy memory missing", b.inputs)
	}
	var memory struct {
		SchemaVersion string       `json:"schema_version"`
		PriorCritique codex.Result `json:"prior_critique"`
	}
	if err := json.Unmarshal(b.inputs[4].Strategies[0], &memory); err != nil || memory.SchemaVersion != "strategy.v1" || memory.PriorCritique.Reply != "A tese depende de um prazo de resgate que não foi comprovado." || len(memory.PriorCritique.Unknowns) != 1 {
		t.Fatal("persisted objection was not recovered", memory, err)
	}
	artifacts, err := s.Strategies(context.Background())
	if err != nil || len(artifacts) != 1 {
		t.Fatal(artifacts, err)
	}
	var artifact map[string]json.RawMessage
	if err := json.Unmarshal(artifacts[0], &artifact); err != nil || artifact["prior_critique"] != nil {
		t.Fatal("memory metadata changed public strategy schema", artifact, err)
	}
}
