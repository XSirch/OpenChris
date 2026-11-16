package council

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"strings"
	"testing"
	"time"

	"openchris/conselho/internal/codex"
	"openchris/conselho/internal/memory"
	"openchris/conselho/internal/pluggy"
)

func investmentFixture(t *testing.T, count int) (*Service, *memory.Store, *fakeProvider, string) {
	t.Helper()
	s, store, p := fixture(t, 30)
	if _, err := store.DB.Exec(context.Background(), `TRUNCATE pluggy_items CASCADE`); err != nil {
		t.Fatal(err)
	}
	s.cfg.InvestmentsEnabled = true
	id := memory.ID()
	_, err := store.DB.Exec(context.Background(), `INSERT INTO pluggy_items(item_id,user_id,status,explicit_owner,consent_expires_at,last_synced_at,coverage) VALUES($1,$2,'connected',true,'2099-01-01',clock_timestamp(),'{"source_products":["INVESTMENTS"],"complete_for_reported_products":true}')`, id, memory.UserID)
	if err != nil {
		t.Fatal(err)
	}
	name := "CDB senha=private-secret CPF 123.456.789-00 CNPJ 12.345.678/0001-00 conta 123456"
	_, err = store.DB.Exec(context.Background(), `INSERT INTO pluggy_investments(user_id,item_id,external_id,name,investment_type,balance,currency,observed_at,content_hash,payload) SELECT $1,$2,'active-'||n,$3,'FIXED_INCOME',0.1,'BRL',clock_timestamp(),'fixture','{"status":"ACTIVE","owner":"private-owner","number":"private-number","access_token":"private-token","issuerCNPJ":"12.345.678/0001-00"}' FROM generate_series(1,$4::int) n`, memory.UserID, id, name, count)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.DB.Exec(context.Background(), `INSERT INTO pluggy_investments(user_id,item_id,external_id,name,investment_type,balance,currency,observed_at,content_hash,payload) VALUES($1,$2,'withdrawn','Resgatado','FIXED_INCOME',999,'BRL',clock_timestamp(),'fixture','{"status":"TOTAL_WITHDRAWAL"}')`, memory.UserID, id)
	if err != nil {
		t.Fatal(err)
	}
	return s, store, p, id
}

func TestInvestmentContextUsesAuthorizedPositionsExactTotalsAndSafeSources(t *testing.T) {
	s, _, p, _ := investmentFixture(t, 3)
	receipt := ingest(t, s, "dashboard", "investments-context", "Helena, analise meus investimentos")
	p.run = func(_ context.Context, in codex.Request) (codex.Result, error) {
		var data struct {
			Investments *pluggy.Portfolio `json:"pluggy_investments"`
			Sources     []source          `json:"sources"`
		}
		if err := json.Unmarshal(in.Context, &data); err != nil {
			t.Fatal(err)
		}
		if data.Investments == nil {
			t.Fatal("investments missing from agent context")
		}
		portfolio := data.Investments
		if portfolio.ActiveCount != 3 || portfolio.WithdrawnCount != 1 || len(portfolio.Positions) != 3 || portfolio.Totals[0].Balance == nil || *portfolio.Totals[0].Balance != "0.3" {
			t.Fatal("wrong portfolio in context", portfolio)
		}
		for _, secret := range []string{"private-secret", "123.456.789-00", "12.345.678/0001-00", "123456", "private-owner", "private-token", "private-number", "payload", "external_id"} {
			if strings.Contains(string(in.Context), secret) {
				t.Fatal("private field reached runner", secret)
			}
		}
		found := false
		for _, ref := range data.Sources {
			if ref.ID == portfolio.SourceID && ref.Type == "investment" && !ref.ObservedAt.IsZero() {
				found = true
			}
		}
		if !found {
			t.Fatal("missing cit-able investment source")
		}
		result := answer(in)
		result.Evidence = []string{portfolio.SourceID}
		return result, nil
	}
	process(t, s)
	a, err := s.Analysis(context.Background(), receipt.ID)
	if err != nil || a.Status != "complete" || a.Result == nil || !strings.HasPrefix(a.Result.Evidence[0], "pluggy:investments:") {
		t.Fatal("investment analysis not published", a, err)
	}
}

func TestInvestmentChangeOrConsentExpiryMakesAnalysisStale(t *testing.T) {
	for _, change := range []string{"balance", "removed", "revoked", "expires-during-run"} {
		t.Run(change, func(t *testing.T) {
			s, store, p, id := investmentFixture(t, 1)
			if change == "expires-during-run" {
				if _, err := store.DB.Exec(context.Background(), `UPDATE pluggy_items SET consent_expires_at=clock_timestamp()+interval '700 milliseconds' WHERE item_id=$1`, id); err != nil {
					t.Fatal(err)
				}
			}
			receipt := ingest(t, s, "dashboard", "investment-stale", "Analise meus investimentos")
			p.run = func(ctx context.Context, in codex.Request) (codex.Result, error) {
				var err error
				switch change {
				case "balance":
					_, err = store.DB.Exec(ctx, `UPDATE pluggy_investments SET balance=7,version=version+1 WHERE item_id=$1`, id)
				case "removed":
					_, err = store.DB.Exec(ctx, `UPDATE pluggy_investments SET active=false WHERE item_id=$1`, id)
				case "revoked":
					_, err = store.DB.Exec(ctx, `UPDATE pluggy_items SET status='revoked' WHERE item_id=$1`, id)
				case "expires-during-run":
					time.Sleep(800 * time.Millisecond)
				}
				if err != nil {
					t.Fatal(err)
				}
				return answer(in), nil
			}
			process(t, s)
			a, err := s.Analysis(context.Background(), receipt.ID)
			if err != nil || a.Status != "stale" || a.Result != nil {
				t.Fatal("published outdated investment context", a, err)
			}
		})
	}
}

func TestInvestmentContextBudgetKeepsFullTotalsAndCountsOmissions(t *testing.T) {
	s, store, p, id := investmentFixture(t, 250)
	if _, err := store.DB.Exec(context.Background(), `UPDATE pluggy_investments SET name=repeat('A',160),payload=payload||jsonb_build_object('issuer',repeat('B',120)) WHERE item_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	ingest(t, s, "dashboard", "investment-budget", "Analise a composição")
	p.run = func(_ context.Context, in codex.Request) (codex.Result, error) {
		var data struct {
			Investments pluggy.Portfolio `json:"pluggy_investments"`
		}
		json.Unmarshal(in.Context, &data)
		if len(in.Context) > codex.MaxContext || data.Investments.ActiveCount != 250 || data.Investments.Totals[0].Balance == nil || *data.Investments.Totals[0].Balance != "25" || data.Investments.Omitted == 0 || data.Investments.Omitted != 250-len(data.Investments.Positions) {
			t.Fatal("lost aggregates or unreported truncation", len(in.Context), data.Investments)
		}
		return answer(in), nil
	}
	process(t, s)
}

type delayedPortfolioTx struct {
	pgx.Tx
	reads   int
	expires time.Time
}

func (tx *delayedPortfolioTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "string_agg(id::text||':'||version::text") {
		tx.reads++
		if tx.reads == 1 {
			if delay := time.Until(tx.expires) + 20*time.Millisecond; delay > 0 {
				time.Sleep(delay)
			}
		}
	}
	return tx.Tx.QueryRow(ctx, sql, args...)
}
func TestInvestmentManifestRevisionMatchesItsSelectionTime(t *testing.T) {
	s, store, _, id := investmentFixture(t, 1)
	expires := time.Now().Add(300 * time.Millisecond)
	if _, err := store.DB.Exec(context.Background(), `UPDATE pluggy_items SET consent_expires_at=$2 WHERE item_id=$1`, id, expires); err != nil {
		t.Fatal(err)
	}
	receipt := ingest(t, s, "dashboard", "selection-time", "Analise meus investimentos")
	tx, err := store.DB.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	wrapped := &delayedPortfolioTx{Tx: tx, expires: expires}
	data, hash, err := buildContext(context.Background(), wrapped, job{EventID: receipt.ID, Text: "Analise meus investimentos"}, true)
	if err != nil || data.Investments == nil || data.Investments.ActiveCount != 1 {
		t.Fatal("missing initial authorized snapshot", err)
	}
	if delay := time.Until(expires) + 20*time.Millisecond; delay > 0 {
		time.Sleep(delay)
	}
	current, err := fingerprint(context.Background(), tx, true)
	if err != nil || current == hash {
		t.Fatal("manifest already used the expired revision for positions selected before expiry", err)
	}
}

func TestInvestmentBusyRetryRevalidatesBeforeSendingContext(t *testing.T) {
	s, store, p, id := investmentFixture(t, 1)
	receipt := ingest(t, s, "dashboard", "busy-consent", "Analise meus investimentos")
	p.run = func(_ context.Context, in codex.Request) (codex.Result, error) {
		if p.calls.Load() == 1 {
			return codex.Result{}, &codex.Error{Kind: "busy", Transient: true}
		}
		return answer(in), nil
	}
	process(t, s)
	if _, err := store.DB.Exec(context.Background(), `UPDATE pluggy_items SET status='revoked' WHERE item_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(context.Background(), `UPDATE council_jobs SET next_run_at=clock_timestamp() WHERE event_id=$1`, receipt.ID); err != nil {
		t.Fatal(err)
	}
	process(t, s)
	a, err := s.Analysis(context.Background(), receipt.ID)
	if err != nil || p.calls.Load() != 1 || a.Status != "stale" || a.Result != nil {
		t.Fatal("retried unauthorized context", p.calls.Load(), a, err)
	}
}

func TestInvestmentConsentExpiryWhilePublicationWaitsRollsBackResult(t *testing.T) {
	s, store, p, id := investmentFixture(t, 1)
	expires := time.Now().Add(800 * time.Millisecond)
	if _, err := store.DB.Exec(context.Background(), `UPDATE pluggy_items SET consent_expires_at=$2 WHERE item_id=$1`, id, expires); err != nil {
		t.Fatal(err)
	}
	receipt := ingest(t, s, "dashboard", "publication-expiry", "Analise meus investimentos")
	locker, err := store.DB.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Rollback(context.Background())
	if _, err = locker.Exec(context.Background(), `LOCK TABLE council_results IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	p.run = func(_ context.Context, in codex.Request) (codex.Result, error) { return answer(in), nil }
	done := make(chan error, 1)
	go func() {
		ok, err := s.ProcessOne(context.Background())
		if !ok && err == nil {
			err = fmt.Errorf("no job processed")
		}
		done <- err
	}()
	blocked := false
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if err = store.DB.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%INSERT INTO council_results%')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("publication did not reach the blocked write before expiry")
	}
	if delay := time.Until(expires) + 30*time.Millisecond; delay > 0 {
		time.Sleep(delay)
	}
	if err = locker.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("publication did not finish")
	}
	a, err := s.Analysis(context.Background(), receipt.ID)
	var outputs int
	store.DB.QueryRow(context.Background(), `SELECT count(*) FROM conversation_events WHERE direction='outgoing'`).Scan(&outputs)
	if err != nil || a.Status != "stale" || a.Result != nil || outputs != 0 {
		t.Fatal("published after consent expiry", a.Status, outputs, err)
	}
}
