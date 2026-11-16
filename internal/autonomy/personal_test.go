package autonomy

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"openchris/conselho/internal/codex"
	"openchris/conselho/internal/finance"
	"openchris/conselho/internal/memory"
	"openchris/conselho/internal/research"
)

func TestPublicResearchDoesNotTriggerFinancialChange(t *testing.T) {
	s, _, _, _ := fixture(t)
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	tick(t, s, at)
	for i := 0; i < 5; i++ {
		process(t, s)
	}
	s.collector = &fakeCollector{docs: []research.Document{{URL: "https://www.bcb.gov.br/new-source", Title: "Nova notícia", Summary: "Pesquisa pública", Kind: "primary", Hash: "new"}}}
	tick(t, s, at.Add(time.Hour))
	status, err := s.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Runs) != 1 {
		t.Fatalf("research triggered %d rounds without a financial change", len(status.Runs))
	}
}

func TestRecommendationNeedsConfirmedContextAndCitedCurrentEvidence(t *testing.T) {
	for _, mode := range []string{"ready", "missing_context", "missing_citation"} {
		t.Run(mode, func(t *testing.T) {
			s, store, p, b := fixture(t)
			ctx := context.Background()
			at := time.Now().UTC().Truncate(time.Microsecond)
			item := memory.ID()
			if _, err := store.DB.Exec(ctx, `INSERT INTO pluggy_items(item_id,user_id,status,explicit_owner,last_synced_at,coverage) VALUES($1,$2,'connected',true,$3,'{"complete_for_reported_products":true}')`, item, memory.UserID, at); err != nil {
				t.Fatal(err)
			}
			if _, err := store.DB.Exec(ctx, `INSERT INTO pluggy_accounts(user_id,item_id,external_id,name,account_type,subtype,balance,currency,observed_at,content_hash,payload) VALUES($1,$2,'checking','Conta','BANK','CHECKING_ACCOUNT',1000,'BRL',$3,'fixture','{}')`, memory.UserID, item, at); err != nil {
				t.Fatal(err)
			}
			zero, target, days := int64(0), int64(600000), 1
			profile := finance.Profile{Confirmed: true, Goals: []finance.Goal{{Name: "Reserva", TargetCents: 600000, TargetDate: "2027-12-31"}}, ReserveCurrentCents: &zero, ReserveTargetCents: &target, Commitments30DaysCents: &zero, MinimumCheckingCents: &zero, ProtectedCheckingCents: &zero, RiskProfile: "conservative", Experience: "beginner", LiquidityDays: &days}
			if _, err := finance.New(store.DB).SaveProfile(ctx, memory.UserID, profile); err != nil {
				t.Fatal(err)
			}
			personal, err := finance.ReadPersonalFinance(ctx, store.DB, memory.UserID, at)
			if err != nil || !personal.CanRecommend {
				t.Fatalf("fixture eligibility: %+v %v", personal, err)
			}
			if mode != "missing_context" {
				b.personal = &personal
			}
			published := at.Add(-time.Hour)
			doc := research.Document{URL: "https://www.bcb.gov.br/current-reference", Title: "Fonte atual", Summary: "Características atuais", Kind: "primary", Hash: "current", PublishedAt: &published, CollectedAt: at}
			s.collector = &verifyingCollector{doc: doc}
			p.research = func(context.Context, codex.ResearchRequest) (codex.ResearchResult, error) {
				return codex.ResearchResult{Summary: "Pesquisa", Sources: []codex.ResearchSource{{URL: doc.URL}}, Unknowns: []string{}, Model: codex.DefaultModel}, nil
			}
			p.run = func(_ context.Context, in codex.Request) (codex.Result, error) {
				out := answer(in)
				out.Unknowns = []string{}
				var c struct {
					Sources []struct {
						ID string `json:"id"`
					} `json:"sources"`
				}
				if err := json.Unmarshal(in.Context, &c); err != nil {
					t.Fatal(err)
				}
				if mode != "missing_citation" && len(c.Sources) > 0 {
					out.Evidence = []string{c.Sources[0].ID}
					out.Strategy.Evidence = out.Evidence
				}
				return out, nil
			}
			tick(t, s, at)
			for i := 0; i < 5; i++ {
				process(t, s)
			}
			detail, err := s.RunDetail(ctx, mustRun(t, s))
			if err != nil {
				t.Fatal(err)
			}
			want := "await_data"
			if mode == "ready" {
				want = "complete"
			}
			if detail.Run.Status != want {
				t.Fatalf("%s: status=%s code=%s want=%s", mode, detail.Run.Status, detail.Run.ErrorCode, want)
			}
		})
	}
}

func TestDailyRoundPrioritizesPersonalFinances(t *testing.T) {
	s, _, _, _ := fixture(t)
	tick(t, s, time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC))
	status, err := s.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Runs) != 1 || status.Runs[0].Topic != "personal_finance" {
		t.Fatalf("personal finances not prioritized: %+v", status.Runs)
	}
}

func TestDailyDigestAtEightDoesNotRequireAnInference(t *testing.T) {
	s, _, p, _ := fixture(t)
	at := time.Date(2026, 11, 13, 10, 59, 0, 0, time.UTC)
	tick(t, s, at)
	count := func() int {
		var n int
		if err := s.db.QueryRow(context.Background(), `SELECT count(*) FROM autonomy_notices`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count() != 0 {
		t.Fatal("digest before 08h")
	}
	tick(t, s, at.Add(time.Minute))
	if count() != 1 || p.calls.Load() != 0 {
		t.Fatalf("digest missing or consumed inference: notices=%d calls=%d", count(), p.calls.Load())
	}
	tick(t, s, at.Add(8*time.Hour))
	if count() != 1 {
		t.Fatal("duplicate daily digest")
	}
}

func TestRoundReservationReleasesUnusedRetriesAndProtectsSynthesis(t *testing.T) {
	s, _, _, _ := fixture(t)
	tick(t, s, time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC))
	for stage := 0; stage < 5; stage++ {
		process(t, s)
		var reserved int
		if err := s.db.QueryRow(context.Background(), `SELECT reserved_calls FROM autonomy_runs`).Scan(&reserved); err != nil {
			t.Fatal(err)
		}
		if want := 8 - stage*2; reserved != want {
			t.Fatalf("after stage %d reserved=%d want=%d", stage, reserved, want)
		}
	}
}

func TestInsufficientBudgetWaitsBeforeStartingAndResumesNextMorning(t *testing.T) {
	s, _, p, _ := fixture(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	if _, err := s.db.Exec(ctx, `INSERT INTO council_attempts(run_id,origin,quota_day) SELECT md5(n::text)::uuid,'autonomy',($1::timestamptz AT TIME ZONE 'America/Sao_Paulo')::date FROM generate_series(1,15)n`, at); err != nil {
		t.Fatal(err)
	}
	tick(t, s, at)
	status, err := s.Status(ctx)
	if err != nil || status.State != "waiting_budget" || len(status.Runs) != 0 || status.ReservedCalls != 0 {
		t.Fatalf("partial round admitted: %+v %v", status, err)
	}
	if ok, err := s.ProcessOne(ctx); err != nil || ok || p.calls.Load() != 0 {
		t.Fatalf("budget invoked model: %v %v", ok, err)
	}
	requested, err := s.RequestRun(ctx)
	if err != nil || requested.Status != "waiting_budget" || requested.ID != "" {
		t.Fatalf("first requested round should wait without error: %+v %v", requested, err)
	}
	tick(t, s, at.AddDate(0, 0, 1))
	status, err = s.Status(ctx)
	if err != nil || status.State != "active" || len(status.Runs) != 1 || status.Runs[0].Topic != "personal_finance" || status.ReservedCalls != 10 {
		t.Fatalf("daily personal reservation missing: %+v %v", status, err)
	}
}
