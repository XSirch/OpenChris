package autonomy

import (
	"context"
	"openchris/conselho/internal/memory"
	"strings"
	"testing"
	"time"
)

func TestDailyDigestDoesNotReplayRegardlessOfDeliveryReceipt(t *testing.T) {
	for _, receipt := range []string{"pending", "cancelled", "failed", "failed_unknown", "sent"} {
		t.Run(receipt, func(t *testing.T) {
			s, _, p, _ := fixture(t)
			ctx := context.Background()
			loc, _ := time.LoadLocation("America/Sao_Paulo")
			at := time.Date(2026, 11, 10, 8, 0, 0, 0, loc)
			tick(t, s, at)
			if _, err := s.db.Exec(ctx, `UPDATE autonomy_notices SET delivery_state=$1 WHERE kind='daily'`, receipt); err != nil {
				t.Fatal(err)
			}
			tick(t, s, at.Add(11*time.Hour))
			var count int
			if err := s.db.QueryRow(ctx, `SELECT count(*) FROM autonomy_notices WHERE kind='daily'`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("duplicate current-day digest: %d %v", count, err)
			}
			// A later day gets its own deterministic summary without replaying
			// an old inference or requiring one to have finished.
			tick(t, s, at.Add(24*time.Hour))
			if err := s.db.QueryRow(ctx, `SELECT count(*) FROM autonomy_notices WHERE kind='daily'`).Scan(&count); err != nil || count != 2 {
				t.Fatalf("new daily digest: %d %v", count, err)
			}
			notices, err := s.PendingNotices(ctx)
			if err != nil || len(notices) != 1 {
				t.Fatalf("old delivery replayed: %+v %v", notices, err)
			}
			if p.calls.Load() != 0 {
				t.Fatal("digest invoked inference")
			}
		})
	}
}

func TestStaleUnsentDigestIsRenewedButRevokedOrAmbiguousIsNot(t *testing.T) {
	for _, state := range []string{"cancelled_context", "cancelled_revoked", "failed_unknown", "sent"} {
		t.Run(state, func(t *testing.T) {
			s, _, _, _ := fixture(t)
			ctx := context.Background()
			at := time.Date(2026, 11, 13, 11, 0, 0, 0, time.UTC)
			tick(t, s, at)
			status, code := state, ""
			if state == "cancelled_context" {
				status, code = "cancelled", "financial_context_changed"
			}
			if state == "cancelled_revoked" {
				status, code = "cancelled", "autonomy_notice_revoked"
			}
			if _, err := s.db.Exec(ctx, `UPDATE autonomy_notices SET delivery_state=$1,cancellation_code=$2,body='obsolete',financial_revision='obsolete'`, status, code); err != nil {
				t.Fatal(err)
			}
			tick(t, s, at.Add(time.Minute))
			var got, body string
			var count int
			if err := s.db.QueryRow(ctx, `SELECT delivery_state,body,(SELECT count(*) FROM autonomy_notices) FROM autonomy_notices`).Scan(&got, &body, &count); err != nil {
				t.Fatal(err)
			}
			if state == "cancelled_context" {
				if got != "pending" || body == "obsolete" {
					t.Fatalf("stale unsent digest not renewed: %s %s", got, body)
				}
			} else if got != status || body != "obsolete" {
				t.Fatal("revoked or uncertain receipt regenerated", got, body)
			}
			if count != 1 {
				t.Fatal("duplicate digest", count)
			}
		})
	}
}

func TestDailyDigestLabelsObsoleteCompletedAnalysisAsHistory(t *testing.T) {
	s, _, _, b := fixture(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 13, 11, 0, 0, 0, time.UTC)
	if _, err := s.db.Exec(ctx, `INSERT INTO autonomy_runs(id,user_id,status,reason,topic,fingerprint,created_at,completed_at) VALUES($1,$2,'complete','initial','personal_finance','old',$3,$3)`, memory.ID(), memory.UserID, at.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	b.fingerprint = "current"
	tick(t, s, at)
	var body string
	if err := s.db.QueryRow(ctx, `SELECT body FROM autonomy_notices`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "histórico") || !strings.Contains(body, "desatualizado") || strings.Contains(body, "Análise pessoal concluída;") {
		t.Fatal("old analysis was presented as current", body)
	}
}
