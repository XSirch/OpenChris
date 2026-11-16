package council

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"openchris/conselho/internal/autonomy"
	"openchris/conselho/internal/codex"
	"openchris/conselho/internal/memory"
	"openchris/conselho/internal/pluggy"
)

func TestAutonomousContextDoesNotForgeOwnerSpeechOrLeakRiskToCritic(t *testing.T) {
	s, store, _, _ := investmentFixture(t, 3)
	ingest(t, s, "dashboard", "owner-private", "Mensagem pessoal que não pertence à pesquisa automática")
	tx, err := store.DB.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	id, round := memory.ID(), memory.ID()
	in := autonomy.ContextInput{TaskID: id, RoundID: round, AgentID: "A21", Stage: 3, Topic: "fixed_income", Evidence: []autonomy.Evidence{{ID: "news:fixture", URL: "https://agenciabrasil.ebc.com.br/economia/teste", Title: "Fonte pública", Summary: "Informação pública consultada.", Kind: "news", ObservedAt: time.Now().UTC()}}, Prior: []autonomy.Report{{AgentID: "A10", Stage: 1, Result: codex.Result{Reply: "Hipótese a verificar.", Episode: "Estratégia em pesquisa."}}, {AgentID: "A17", Stage: 2, Result: codex.Result{Reply: "RISK-REPORT-MUST-NOT-REACH-CRITIC", Episode: "Avaliação de risco independente."}}}}
	builder := NewAutonomousBuilder(true)
	snapshot, err := builder.Build(context.Background(), tx, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"message:0", "Mensagem pessoal que não pertence", "RISK-REPORT-MUST-NOT-REACH-CRITIC", "private-secret", "123.456.789-00", "12.345.678/0001-00", "private-number", "private-token"} {
		if strings.Contains(string(snapshot.Context), private) {
			t.Fatal("untrusted/private authority in automatic context", private)
		}
	}
	if !strings.Contains(string(snapshot.Context), "task:"+id) || !strings.Contains(string(snapshot.Context), "news:fixture") || !strings.Contains(string(snapshot.Context), "Hipótese a verificar.") {
		t.Fatal("missing real task/source/strategy context")
	}
	var data contextData
	if err = json.Unmarshal(snapshot.Context, &data); err != nil {
		t.Fatal(err)
	}
	if data.Profile.ID != "A21" || data.Investments == nil || data.Investments.ActiveCount != 3 {
		t.Fatal("wrong profile or missing authorized holdings")
	}
	current, err := builder.Fingerprint(context.Background(), tx, data.AsOf)
	if err != nil || current != snapshot.Fingerprint {
		t.Fatal("revision differs from selected context", err)
	}
	var count int
	if err = tx.QueryRow(context.Background(), `SELECT count(*) FROM conversation_events`).Scan(&count); err != nil || count != 1 {
		t.Fatal("automatic context fabricated journal events", count, err)
	}
}

func TestAutonomousContextBudgetRetainsFullPortfolioAggregates(t *testing.T) {
	_, store, _, _ := investmentFixture(t, 250)
	tx, err := store.DB.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	in := autonomy.ContextInput{TaskID: memory.ID(), RoundID: memory.ID(), AgentID: "A01", Stage: 4, Topic: "funds"}
	for i := 0; i < 12; i++ {
		in.Evidence = append(in.Evidence, autonomy.Evidence{ID: "news:" + strings.Repeat("x", i+1), URL: "https://agenciabrasil.ebc.com.br/economia/" + strings.Repeat("u", 200), Title: strings.Repeat("N", 180), Summary: strings.Repeat("S", 600), Kind: "news", ObservedAt: time.Now().UTC()})
	}
	for stage := 1; stage < 4; stage++ {
		in.Prior = append(in.Prior, autonomy.Report{AgentID: "A10", Stage: stage, Result: codex.Result{Reply: strings.Repeat("parecer ", 900), Episode: strings.Repeat("resumo ", 200), Unknowns: []string{"Ausência de limites configurados."}}})
	}
	got, err := NewAutonomousBuilder(true).Build(context.Background(), tx, in)
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Investments pluggy.Portfolio `json:"pluggy_investments"`
	}
	json.Unmarshal(got.Context, &data)
	if len(got.Context) > codex.MaxContext || data.Investments.ActiveCount != 250 || data.Investments.Totals[0].Balance == nil || *data.Investments.Totals[0].Balance != "25" || data.Investments.Omitted < 150 {
		t.Fatal("budget lost aggregate coverage", len(got.Context))
	}
}

func TestAutomaticReviewsReceiveStructuredStrategyWithoutProseDuplication(t *testing.T) {
	_, store, _ := fixture(t, 30)
	ctx := context.Background()
	tx, err := store.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	for _, stage := range []int{2, 3, 4} {
		draft := &codex.StrategyDraft{Title: "Hipótese estruturada", Hypothesis: "Hipótese somente na ficha", Mandate: "long_term", TimeHorizon: "Doze meses", Conditions: []string{"CONDITION-ONLY-IN-FIELD"}, InvalidationConditions: []string{"INVALIDATION-ONLY-IN-FIELD"}, EligibleInstruments: []string{"INSTRUMENT-ONLY-IN-FIELD"}, Risks: []string{"RISK-ONLY-IN-FIELD"}, Evidence: []string{"news:fixture"}}
		in := autonomy.ContextInput{TaskID: memory.ID(), RoundID: memory.ID(), AgentID: "A17", Stage: stage, Topic: "fixed_income", Prior: []autonomy.Report{{AgentID: "A10", Stage: 1, Result: codex.Result{Reply: "Consulte a ficha estruturada.", Episode: "Estudo criado.", Strategy: draft, Evidence: []string{"news:fixture"}}}}}
		got, err := NewAutonomousBuilder(false).Build(ctx, tx, in)
		if err != nil {
			t.Fatal(err)
		}
		for _, expected := range []string{"CONDITION-ONLY-IN-FIELD", "INVALIDATION-ONLY-IN-FIELD", "INSTRUMENT-ONLY-IN-FIELD", "RISK-ONLY-IN-FIELD", "news:fixture"} {
			if !strings.Contains(string(got.Context), expected) {
				t.Fatalf("stage%d loses %s", stage, expected)
			}
		}
	}
}

func TestNextAutomaticRoundRecoversConditionsAndPriorObjections(t *testing.T) {
	_, store, _ := fixture(t, 30)
	ctx := context.Background()
	tx, err := store.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	raw := json.RawMessage(`{"title":"Estudo anterior","hypothesis":"Hipótese anterior","version":2,"status":"research","conditions":["PREVIOUS-CONDITION"],"invalidation_conditions":["PREVIOUS-INVALIDATION"],"eligible_instruments":["PREVIOUS-INSTRUMENT"],"risks":["PREVIOUS-RISK"],"prior_critique":{"reply":"PREVIOUS-OBJECTION · CPF123.456.789-00","episode":"Crítica anterior","evidence":[],"unknowns":["Perfil não confirmado"],"assumptions":[]}}`)
	got, err := NewAutonomousBuilder(false).Build(ctx, tx, autonomy.ContextInput{TaskID: memory.ID(), RoundID: memory.ID(), AgentID: "A10", Stage: 1, Topic: "fixed_income", Strategies: []json.RawMessage{raw}})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"PREVIOUS-CONDITION", "PREVIOUS-INVALIDATION", "PREVIOUS-INSTRUMENT", "PREVIOUS-RISK", "PREVIOUS-OBJECTION"} {
		if !strings.Contains(string(got.Context), expected) {
			t.Fatalf("next round loses %s", expected)
		}
	}
	if strings.Contains(string(got.Context), "123.456.789-00") {
		t.Fatal("prior critique disclosed personal identifier")
	}
}
