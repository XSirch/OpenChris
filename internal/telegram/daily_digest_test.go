package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"openchris/conselho/internal/autonomy"
	"openchris/conselho/internal/finance"
	"openchris/conselho/internal/memory"
)

func TestDailyDigestQueuesWithoutCompletedInferenceAndSendsOnce(t *testing.T) {
	db := autonomyTestPool(t)
	ctx := context.Background()
	sends := 0
	var received struct {
		Text string `json:"text"`
	}
	s := autonomyLocalService(t, db, nil, func(w http.ResponseWriter, r *http.Request) {
		sends++
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":333}}`)
	})
	s.now = func() time.Time { return time.Date(2030, 1, 2, 11, 0, 0, 0, time.UTC) }
	id := memory.ID()
	if _, err := db.Exec(ctx, `INSERT INTO autonomy_state(user_id,notice_epoch) VALUES($1,$2)`, memory.UserID, s.now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	body := "Gastos do mês (BRL): 125,50.\nValor disponível para investir: depende das pendências abaixo.\n• Defina seus objetivos."
	personal, err := finance.ReadPersonalFinance(ctx, db, memory.UserID, s.now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO autonomy_notices(id,user_id,kind,slot_key,created_at,body,financial_revision) VALUES($1,$2,'daily','2030-01-02',$3,$4,$5)`, id, memory.UserID, s.now(), body, personal.Revision); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.QueueAutonomyNotice(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := s.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if sends != 1 || !strings.Contains(received.Text, body) || !strings.Contains(received.Text, "https://example.test/reports") || !strings.Contains(received.Text, "https://example.test/finance/profile") {
		t.Fatalf("digest sends=%d body=%s", sends, received.Text)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM conversation_events)+(SELECT count(*) FROM autonomy_runs)+(SELECT count(*) FROM council_attempts)`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("digest created inference records: %d %v", count, err)
	}
}

type digestTestBuilder struct{}

func (digestTestBuilder) Fingerprint(context.Context, pgx.Tx, time.Time) (string, error) {
	return "digest-fixture", nil
}
func (digestTestBuilder) Build(context.Context, pgx.Tx, autonomy.ContextInput) (autonomy.ContextSnapshot, error) {
	panic("digest must not infer")
}

func TestDailyDigestRefreshBeforeQueueOrSendRetainsSingleReceipt(t *testing.T) {
	for _, phase := range []string{"before_queue", "before_send"} {
		t.Run(phase, func(t *testing.T) {
			db := autonomyTestPool(t)
			ctx := context.Background()
			sends := 0
			s := autonomyLocalService(t, db, nil, func(w http.ResponseWriter, _ *http.Request) {
				sends++
				fmt.Fprint(w, `{"ok":true,"result":{"message_id":333}}`)
			})
			at := time.Date(2030, 1, 2, 11, 0, 0, 0, time.UTC)
			s.now = func() time.Time { return at }
			if _, err := db.Exec(ctx, `TRUNCATE finance_profile CASCADE`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `INSERT INTO autonomy_state(user_id,notice_epoch,next_collect_at,next_run_at) VALUES($1,$2,$3,$3)`, memory.UserID, at.Add(-time.Hour), at.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			a := autonomy.New(db, nil, digestTestBuilder{}, autonomy.Config{Enabled: true})
			if err := a.Tick(ctx, at); err != nil {
				t.Fatal(err)
			}
			var id, oldRevision string
			if err := db.QueryRow(ctx, `SELECT id::text,financial_revision FROM autonomy_notices`).Scan(&id, &oldRevision); err != nil {
				t.Fatal(err)
			}
			if phase == "before_send" {
				if err := s.QueueAutonomyNotice(ctx, id); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := finance.New(db).SaveProfile(ctx, memory.UserID, finance.Profile{RiskProfile: "conservative"}); err != nil {
				t.Fatal(err)
			}
			if phase == "before_queue" {
				if err := s.QueueAutonomyNotice(ctx, id); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := s.ProcessOne(ctx); err != nil {
					t.Fatal(err)
				}
			}
			var code string
			if err := db.QueryRow(ctx, `SELECT cancellation_code FROM autonomy_notices WHERE id=$1`, id).Scan(&code); err != nil || code != "financial_context_changed" || sends != 0 {
				t.Fatalf("invalid unsent receipt: %s %d %v", code, sends, err)
			}
			at = at.Add(time.Minute)
			if err := a.Tick(ctx, at); err != nil {
				t.Fatal(err)
			}
			var newRevision, state string
			var count int
			if err := db.QueryRow(ctx, `SELECT financial_revision,delivery_state,(SELECT count(*) FROM autonomy_notices) FROM autonomy_notices WHERE id=$1`, id).Scan(&newRevision, &state, &count); err != nil || newRevision == oldRevision || state != "pending" || count != 1 {
				t.Fatalf("digest not renewed: %s %s count=%d %v", newRevision, state, count, err)
			}
			if err := s.QueueAutonomyNotice(ctx, id); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			if err := a.Tick(ctx, at.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			if err := s.QueueAutonomyNotice(ctx, id); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			if sends != 1 {
				t.Fatal("duplicate or missing regenerated digest", sends)
			}
		})
	}
}

func TestDailyDigestRevalidatesPauseIdentityAndDay(t *testing.T) {
	for _, change := range []string{"pause", "identity", "day", "body", "finances"} {
		t.Run(change, func(t *testing.T) {
			db := autonomyTestPool(t)
			ctx := context.Background()
			sends := 0
			s := autonomyLocalService(t, db, nil, func(w http.ResponseWriter, _ *http.Request) {
				sends++
				fmt.Fprint(w, `{"ok":true,"result":{"message_id":333}}`)
			})
			s.now = func() time.Time { return time.Date(2030, 1, 2, 11, 0, 0, 0, time.UTC) }
			id := memory.ID()
			if _, err := db.Exec(ctx, `INSERT INTO autonomy_state(user_id,notice_epoch) VALUES($1,$2)`, memory.UserID, s.now().Add(-time.Hour)); err != nil {
				t.Fatal(err)
			}
			personal, err := finance.ReadPersonalFinance(ctx, db, memory.UserID, s.now())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `INSERT INTO autonomy_notices(id,user_id,kind,slot_key,created_at,body,financial_revision) VALUES($1,$2,'daily','2030-01-02',$3,'Resumo de gastos',$4)`, id, memory.UserID, s.now(), personal.Revision); err != nil {
				t.Fatal(err)
			}
			if err := s.QueueAutonomyNotice(ctx, id); err != nil {
				t.Fatal(err)
			}
			query := ""
			switch change {
			case "pause":
				query = `UPDATE autonomy_state SET paused=true`
			case "identity":
				query = `UPDATE telegram_autonomy_identity SET accepted_user_id=999`
			case "body":
				query = `UPDATE autonomy_notices SET body='Texto alterado após enfileirar'`
			case "finances":
				query = `UPDATE autonomy_notices SET financial_revision='obsolete'`
			case "day":
				s.now = func() time.Time { return time.Date(2030, 1, 3, 11, 0, 0, 0, time.UTC) }
			}
			if query != "" {
				if _, err := db.Exec(ctx, query); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			var state string
			if err := db.QueryRow(ctx, `SELECT delivery_state FROM autonomy_notices WHERE id=$1`, id).Scan(&state); err != nil || state != "cancelled" || sends != 0 {
				t.Fatalf("revoked digest state=%s sends=%d %v", state, sends, err)
			}
		})
	}
}
