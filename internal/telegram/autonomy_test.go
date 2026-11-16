package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"openchris/conselho/internal/memory"
	"openchris/conselho/migrations"
)

func autonomyTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	db := testPool(t)
	if _, err := db.Exec(context.Background(), `TRUNCATE autonomy_state,autonomy_runs,autonomy_strategies CASCADE`); err != nil {
		t.Fatal(err)
	}
	return db
}

func autonomyLocalService(t *testing.T, db *pgxpool.Pool, reply ReplyBuilder, handler http.HandlerFunc) *Service {
	t.Helper()
	base := localService(t, db, reply, handler)
	cfg := base.cfg
	cfg.AutonomyEnabled = true
	service, err := NewWithClient(db, cfg, reply, base.client)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func noticeFixture(t *testing.T, db *pgxpool.Pool, service *Service, status, kind string) (string, string) {
	t.Helper()
	service.now = func() time.Time { return time.Date(2030, 1, 2, 15, 0, 0, 0, time.UTC) }
	at := service.now()
	ctx := context.Background()
	noticeID, runID := memory.ID(), memory.ID()
	if _, err := db.Exec(ctx, `INSERT INTO autonomy_state(user_id,notice_epoch) VALUES($1,$2) ON CONFLICT(user_id) DO UPDATE SET paused=false,notice_epoch=$2`, memory.UserID, at.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO autonomy_runs(id,user_id,status,reason,topic,fingerprint,created_at,completed_at,error_code) VALUES($1,$2,$3,'periodic','brazil_macro','synthetic',$4,$5,'operational_code')`, runID, memory.UserID, status, at.Add(-30*time.Minute), at.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if status == "complete" || status == "await_data" {
		for stage := 0; stage < 5; stage++ {
			if _, err := db.Exec(ctx, `INSERT INTO autonomy_tasks(id,run_id,stage,agent_id,status,result,completed_at) VALUES($1,$2,$3,'atlas','complete',$4,$5)`, memory.ID(), runID, stage, `{"summary":"Private model text must never reach Telegram"}`, at.Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
		strategyID := memory.ID()
		if _, err := db.Exec(ctx, `INSERT INTO autonomy_strategies(id,user_id,identity_key,created_at) VALUES($1,$2,$1::uuid::text,$3)`, strategyID, memory.UserID, at.Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO autonomy_strategy_versions(strategy_id,version,run_id,artifact,critique,created_at) VALUES($1,1,$2,'{}','{}',$3)`, strategyID, runID, at.Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `INSERT INTO autonomy_notices(id,user_id,run_id,kind,slot_key,created_at) VALUES($1,$2,$3,$4,$1::uuid::text,$5)`, noticeID, memory.UserID, runID, kind, at); err != nil {
		t.Fatal(err)
	}
	return noticeID, runID
}

func TestAutonomyNoticeQueuesAndSendsFixedTextWithoutConversation(t *testing.T) {
	db := autonomyTestPool(t)
	ctx := context.Background()
	sends := 0
	var received struct {
		ChatID int64  `json:"chat_id"`
		Text   string `json:"text"`
	}
	s := autonomyLocalService(t, db, nil, func(w http.ResponseWriter, r *http.Request) {
		sends++
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":333}}`)
	})
	noticeID, runID := noticeFixture(t, db, s, "await_data", "summary")
	for i := 0; i < 2; i++ {
		if err := s.QueueAutonomyNotice(ctx, noticeID); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM telegram_outbox`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("queued outbox count=%d, err=%v", count, err)
	}
	if processed, err := s.ProcessOne(ctx); err != nil || !processed {
		t.Fatalf("process=%v, %v", processed, err)
	}
	if _, err := s.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if sends != 1 || received.ChatID != 456 || received.Text != "Há uma nova rodada autônoma no CONSELHO. Consulte o painel: https://example.test/dashboard?run="+runID {
		t.Fatalf("unsafe or duplicate send: count=%d payload=%+v", sends, received)
	}
	var state string
	if err := db.QueryRow(ctx, `SELECT delivery_state FROM autonomy_notices WHERE id=$1`, noticeID).Scan(&state); err != nil || state != "sent" {
		t.Fatalf("notice state=%s, %v", state, err)
	}
	if err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM conversation_events)+(SELECT count(*) FROM episodes)+(SELECT count(*) FROM telegram_inbox)`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("fabricated conversation records=%d, %v", count, err)
	}
}

func TestAutonomyNoticeQueueRevalidatesAuthorityTimeAndArtifact(t *testing.T) {
	for _, tc := range []struct {
		name, sql string
		quiet     bool
	}{
		{"paused", `UPDATE autonomy_state SET paused=true`, false},
		{"notice before epoch", `UPDATE autonomy_state SET notice_epoch=notice_epoch+interval '2 hours'`, false},
		{"old artifact", `UPDATE autonomy_runs SET completed_at=created_at-interval '2 hours'`, false},
		{"previous day", `UPDATE autonomy_notices SET created_at=created_at-interval '1 day'; UPDATE autonomy_runs SET created_at=created_at-interval '1 day',completed_at=completed_at-interval '1 day'; UPDATE autonomy_state SET notice_epoch=notice_epoch-interval '2 days'`, false},
		{"future notice", `UPDATE autonomy_notices SET created_at=created_at+interval '1 hour'`, false},
		{"nonterminal run", `UPDATE autonomy_runs SET status='running',completed_at=NULL`, false},
		{"missing strategy", `DELETE FROM autonomy_strategy_versions`, false},
		{"missing critique", `UPDATE autonomy_tasks SET status='failed' WHERE stage=3`, false},
		{"quiet hours", ``, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := autonomyTestPool(t)
			s := autonomyLocalService(t, db, nil, nil)
			noticeID, _ := noticeFixture(t, db, s, "complete", "summary")
			if tc.sql != "" {
				if _, err := db.Exec(context.Background(), tc.sql); err != nil {
					t.Fatal(err)
				}
			}
			if tc.quiet {
				s.now = func() time.Time { return time.Date(2030, 1, 3, 1, 0, 0, 0, time.UTC) }
			}
			if err := s.QueueAutonomyNotice(context.Background(), noticeID); err != nil {
				t.Fatal(err)
			}
			var queued int
			var state string
			if err := db.QueryRow(context.Background(), `SELECT delivery_state,(SELECT count(*) FROM telegram_outbox) FROM autonomy_notices WHERE id=$1`, noticeID).Scan(&state, &queued); err != nil {
				t.Fatal(err)
			}
			wantState := "cancelled"
			if tc.quiet {
				wantState = "pending"
			}
			if queued != 0 || state != wantState {
				t.Fatalf("unavailable notice queued: count=%d, state=%s want=%s", queued, state, wantState)
			}
		})
	}
}

func TestAutonomyNoticeSendRechecksClaimAndDoesNotRetarget(t *testing.T) {
	for _, mutation := range []string{"pause", "epoch", "owner", "owner changed back", "old day", "tampered destination", "tampered body", "artifact removed", "cancelled notice"} {
		t.Run(mutation, func(t *testing.T) {
			db := autonomyTestPool(t)
			ctx := context.Background()
			sends := 0
			s := autonomyLocalService(t, db, nil, func(w http.ResponseWriter, r *http.Request) {
				sends++
				fmt.Fprint(w, `{"ok":true,"result":{"message_id":444}}`)
			})
			noticeID, _ := noticeFixture(t, db, s, "complete", "summary")
			if err := s.QueueAutonomyNotice(ctx, noticeID); err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "pause":
				_, _ = db.Exec(ctx, `UPDATE autonomy_state SET paused=true`)
			case "epoch":
				_, _ = db.Exec(ctx, `UPDATE autonomy_state SET notice_epoch=notice_epoch+interval '1 minute'`)
			case "owner", "owner changed back":
				cfg := s.cfg
				cfg.AllowedUserID, cfg.AllowedChatID = 789, 789
				next, err := NewWithClient(db, cfg, nil, s.client)
				if err != nil {
					t.Fatal(err)
				}
				next.now = s.now
				if mutation == "owner changed back" {
					next, err = NewWithClient(db, s.cfg, nil, s.client)
					if err != nil {
						t.Fatal(err)
					}
					next.now = s.now
				}
				s = next
			case "old day":
				s.now = func() time.Time { return time.Date(2030, 1, 3, 15, 0, 0, 0, time.UTC) }
			case "tampered destination":
				_, _ = db.Exec(ctx, `UPDATE telegram_outbox SET chat_id=999`)
			case "tampered body":
				_, _ = db.Exec(ctx, `UPDATE telegram_outbox SET body='Private model output must not be sent'`)
			case "artifact removed":
				_, _ = db.Exec(ctx, `DELETE FROM autonomy_strategy_versions`)
			case "cancelled notice":
				_, _ = db.Exec(ctx, `UPDATE autonomy_notices SET delivery_state='cancelled'`)
			}
			if _, err := s.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			var state, outboxState string
			if err := db.QueryRow(ctx, `SELECT n.delivery_state,o.status FROM autonomy_notices n JOIN telegram_outbox o ON o.notification_id=n.id WHERE n.id=$1`, noticeID).Scan(&state, &outboxState); err != nil {
				t.Fatal(err)
			}
			if sends != 0 || state != "cancelled" || outboxState != "failed" {
				t.Fatalf("revoked notice escaped: sends=%d notice=%s outbox=%s", sends, state, outboxState)
			}
		})
	}
}

func TestAutonomyNoticePauseAfterClaimBlocksHTTP(t *testing.T) {
	db := autonomyTestPool(t)
	ctx := context.Background()
	sends := 0
	s := autonomyLocalService(t, db, nil, func(w http.ResponseWriter, r *http.Request) {
		sends++
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":555}}`)
	})
	noticeID, _ := noticeFixture(t, db, s, "complete", "summary")
	if err := s.QueueAutonomyNotice(ctx, noticeID); err != nil {
		t.Fatal(err)
	}
	item, claimed, err := s.claimAutonomyOne(ctx)
	if err != nil || !claimed {
		t.Fatalf("claim=%v %v", claimed, err)
	}
	if _, err := db.Exec(ctx, `UPDATE autonomy_state SET paused=true`); err != nil {
		t.Fatal(err)
	}
	if err := s.sendClaimed(ctx, item); err != nil {
		t.Fatal(err)
	}
	if sends != 0 {
		t.Fatal("paused claim reached Telegram HTTP")
	}
}

func TestAutonomyNoticeQuietHoursDeferSameDayAndDoNotCatchUp(t *testing.T) {
	for _, nextDay := range []bool{false, true} {
		t.Run(fmt.Sprint(nextDay), func(t *testing.T) {
			db := autonomyTestPool(t)
			ctx := context.Background()
			sends := 0
			s := autonomyLocalService(t, db, nil, func(w http.ResponseWriter, r *http.Request) {
				sends++
				fmt.Fprint(w, `{"ok":true,"result":{"message_id":666}}`)
			})
			noticeID, _ := noticeFixture(t, db, s, "complete", "summary")
			if err := s.QueueAutonomyNotice(ctx, noticeID); err != nil {
				t.Fatal(err)
			}
			s.now = func() time.Time { return time.Date(2030, 1, 3, 1, 0, 0, 0, time.UTC) }
			if _, err := s.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			if sends != 0 {
				t.Fatal("quiet-hours notice reached HTTP")
			}
			if nextDay {
				s.now = func() time.Time { return time.Date(2030, 1, 3, 12, 0, 0, 0, time.UTC) }
			} else {
				s.now = func() time.Time { return time.Date(2030, 1, 3, 0, 59, 0, 0, time.UTC) }
			}
			if _, err := s.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			want := 1
			if nextDay {
				want = 0
			}
			if sends != want {
				t.Fatalf("defer/catch-up sends=%d want=%d", sends, want)
			}
		})
	}
}

func TestAutonomyNoticeConcurrentQueueAndFixedAlert(t *testing.T) {
	db := autonomyTestPool(t)
	ctx := context.Background()
	var text string
	s := autonomyLocalService(t, db, nil, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		text = body.Text
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":777}}`)
	})
	noticeID, runID := noticeFixture(t, db, s, "partial", "alert")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.QueueAutonomyNotice(ctx, noticeID); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if _, err := s.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM telegram_outbox`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate notice rows=%d %v", count, err)
	}
	if text != "Há uma pendência na operação autônoma do CONSELHO. Consulte o painel: https://example.test/dashboard?run="+runID || strings.Contains(text, "operational_code") {
		t.Fatalf("alert text=%q", text)
	}
}

func TestAutonomyNoticeDeliveryOutcomesPreserveRetryPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, response, wantNotice, wantOutbox string
		status, wantSends                      int
	}{
		{"rate limit then success", `{"ok":false,"error_code":429,"parameters":{"retry_after":30}}`, "sent", "sent", 429, 2},
		{"known rejection then success", `{"ok":false,"error_code":503}`, "sent", "sent", 503, 2},
		{"ambiguous response", `not JSON`, "failed_unknown", "unknown", 200, 1},
		{"permanent rejection", `{"ok":false,"error_code":403}`, "failed", "failed", 403, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := autonomyTestPool(t)
			ctx := context.Background()
			sends := 0
			s := autonomyLocalService(t, db, nil, func(w http.ResponseWriter, r *http.Request) {
				sends++
				if sends == 1 {
					w.WriteHeader(tc.status)
					fmt.Fprint(w, tc.response)
					return
				}
				fmt.Fprint(w, `{"ok":true,"result":{"message_id":888}}`)
			})
			noticeID, _ := noticeFixture(t, db, s, "complete", "summary")
			if err := s.QueueAutonomyNotice(ctx, noticeID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `UPDATE telegram_outbox SET next_attempt_at=clock_timestamp()`); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if _, err := s.ProcessOne(ctx); err != nil {
					t.Fatal(err)
				}
			}
			var notice, outbox string
			if err := db.QueryRow(ctx, `SELECT n.delivery_state,o.status FROM autonomy_notices n JOIN telegram_outbox o ON o.notification_id=n.id WHERE n.id=$1`, noticeID).Scan(&notice, &outbox); err != nil {
				t.Fatal(err)
			}
			if sends != tc.wantSends || notice != tc.wantNotice || outbox != tc.wantOutbox {
				t.Fatalf("delivery sends=%d notice=%s outbox=%s", sends, notice, outbox)
			}
		})
	}
}

func TestAutonomyNoticeRetryCapAndExpiredClaimNeverResend(t *testing.T) {
	for _, abandoned := range []bool{false, true} {
		t.Run(fmt.Sprint(abandoned), func(t *testing.T) {
			db := autonomyTestPool(t)
			ctx := context.Background()
			sends := 0
			s := autonomyLocalService(t, db, nil, func(w http.ResponseWriter, r *http.Request) {
				sends++
				w.WriteHeader(429)
				fmt.Fprint(w, `{"ok":false,"error_code":429}`)
			})
			noticeID, _ := noticeFixture(t, db, s, "complete", "summary")
			if err := s.QueueAutonomyNotice(ctx, noticeID); err != nil {
				t.Fatal(err)
			}
			if abandoned {
				if _, claimed, err := s.claimAutonomyOne(ctx); err != nil || !claimed {
					t.Fatalf("claim=%v %v", claimed, err)
				}
				if _, err := db.Exec(ctx, `UPDATE telegram_outbox SET claimed_at=clock_timestamp()-interval '2 minutes'`); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 7; i++ {
				if _, err := s.ProcessOne(ctx); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(ctx, `UPDATE telegram_outbox SET next_attempt_at=clock_timestamp()`); err != nil {
					t.Fatal(err)
				}
			}
			wantSends, wantNotice, wantOutbox := 5, "failed", "failed"
			if abandoned {
				wantSends, wantNotice, wantOutbox = 0, "failed_unknown", "unknown"
			}
			var notice, outbox string
			if err := db.QueryRow(ctx, `SELECT n.delivery_state,o.status FROM autonomy_notices n JOIN telegram_outbox o ON o.notification_id=n.id WHERE n.id=$1`, noticeID).Scan(&notice, &outbox); err != nil {
				t.Fatal(err)
			}
			if sends != wantSends || notice != wantNotice || outbox != wantOutbox {
				t.Fatalf("retry/unknown sends=%d notice=%s outbox=%s", sends, notice, outbox)
			}
		})
	}
}

func TestAutonomyNoticeConcurrentWorkersSendOnce(t *testing.T) {
	db := autonomyTestPool(t)
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	s := autonomyLocalService(t, db, nil, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":999}}`)
	})
	noticeID, _ := noticeFixture(t, db, s, "complete", "summary")
	if err := s.QueueAutonomyNotice(ctx, noticeID); err != nil {
		t.Fatal(err)
	}
	first := make(chan error, 1)
	go func() { _, err := s.ProcessOne(ctx); first <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("first worker did not reach HTTP")
	}
	processed, err := s.ProcessOne(ctx)
	close(release)
	if err != nil || processed {
		t.Fatalf("second worker repeated active delivery: processed=%v err=%v", processed, err)
	}
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

func TestAutonomyTelegramMigrationPreservesLegacyAndReplaysTwice(t *testing.T) {
	db := autonomyTestPool(t)
	ctx := context.Background()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	schema := "telegram_upgrade_" + strings.ReplaceAll(memory.ID(), "-", "")
	if _, err := tx.Exec(ctx, `CREATE SCHEMA `+schema+`; SET LOCAL search_path TO `+schema); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"001_memory.sql", "002_finance.sql", "003_telegram.sql", "004_pluggy.sql", "005_council.sql", "006_autonomy.sql"} {
		sql, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	var eventID int64
	if err := tx.QueryRow(ctx, `INSERT INTO conversation_events(user_id,channel,external_key,direction,body) VALUES($1,'telegram','legacy','incoming','Preserved legacy message') RETURNING id`, memory.UserID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO telegram_inbox(bot_id,update_id,payload_hash,incoming_event_id,command) VALUES(123456,7,$1,$2,'/start')`, strings.Repeat("a", 64), eventID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO telegram_outbox(bot_id,update_id,incoming_event_id,chat_id,body) VALUES(123456,7,$1,456,'Preserved legacy reply')`, eventID); err != nil {
		t.Fatal(err)
	}
	sql, err := migrations.Files.ReadFile("007_autonomy_telegram.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("migration replay %d: %v", i, err)
		}
	}
	var id int64
	var body string
	if err := tx.QueryRow(ctx, `SELECT id,body FROM telegram_outbox WHERE bot_id=123456 AND update_id=7`).Scan(&id, &body); err != nil || id <= 0 || body != "Preserved legacy reply" {
		t.Fatalf("legacy row changed id=%d body=%q %v", id, body, err)
	}
	for i := 0; i < 2; i++ {
		noticeID := memory.ID()
		if _, err := tx.Exec(ctx, `INSERT INTO autonomy_notices(id,user_id,kind,slot_key) VALUES($1,$2,'alert',$1::uuid::text)`, noticeID, memory.UserID); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO telegram_outbox(bot_id,chat_id,body,notification_id,accepted_user_id,accepted_chat_id,notice_epoch,identity_epoch) VALUES(123456,456,'Fixed notice',$1,456,456,clock_timestamp(),clock_timestamp())`, noticeID); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM telegram_outbox WHERE update_id IS NULL AND incoming_event_id IS NULL`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("notification rows=%d %v", count, err)
	}
}

func TestAutonomyDisabledCancelsOldNoticeAndPreservesManualTelegram(t *testing.T) {
	db := autonomyTestPool(t)
	ctx := context.Background()
	sends := 0
	s := autonomyLocalService(t, db, nil, func(w http.ResponseWriter, r *http.Request) {
		sends++
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":1001}}`)
	})
	noticeID, _ := noticeFixture(t, db, s, "complete", "summary")
	if err := s.QueueAutonomyNotice(ctx, noticeID); err != nil {
		t.Fatal(err)
	}
	cfg := s.cfg
	cfg.AutonomyEnabled = false
	next, err := NewWithClient(db, cfg, nil, s.client)
	if err != nil {
		t.Fatal(err)
	}
	next.now = s.now
	if err := next.QueueAutonomyNotice(ctx, noticeID); !errors.Is(err, ErrDisabled) {
		t.Errorf("disabled queue error = %v", err)
	}
	if _, err := next.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if sends != 0 {
		t.Errorf("disabled autonomy sent %d notifications", sends)
	}
	var state string
	if err := db.QueryRow(ctx, `SELECT delivery_state FROM autonomy_notices WHERE id=$1`, noticeID).Scan(&state); err != nil || state != "cancelled" {
		t.Errorf("disabled queued notice state=%s err=%v", state, err)
	}
	if _, err := next.HandleUpdate(ctx, cfg.WebhookSecret, privateUpdate(1001, "/start")); err != nil {
		t.Fatal(err)
	}
	if _, err := next.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if sends != 1 {
		t.Fatalf("manual Telegram blocked or notification escaped: sends=%d", sends)
	}
}

func TestAutonomyDisabledAfterClaimBlocksHTTP(t *testing.T) {
	db := autonomyTestPool(t)
	ctx := context.Background()
	sends := 0
	s := autonomyLocalService(t, db, nil, func(w http.ResponseWriter, r *http.Request) {
		sends++
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":1002}}`)
	})
	noticeID, _ := noticeFixture(t, db, s, "complete", "summary")
	if err := s.QueueAutonomyNotice(ctx, noticeID); err != nil {
		t.Fatal(err)
	}
	item, claimed, err := s.claimAutonomyOne(ctx)
	if err != nil || !claimed {
		t.Fatalf("claim=%v %v", claimed, err)
	}
	cfg := s.cfg
	cfg.AutonomyEnabled = false
	next, err := NewWithClient(db, cfg, nil, s.client)
	if err != nil {
		t.Fatal(err)
	}
	next.now = s.now
	if err := next.sendClaimed(ctx, item); err != nil {
		t.Fatal(err)
	}
	if sends != 0 {
		t.Fatal("disabled claimed notification reached HTTP")
	}
}

func TestAutonomyIdentityChangeBeforeQueueNeverRetargetsOldArtifact(t *testing.T) {
	for _, target := range []string{"new service", "old service", "changed back"} {
		t.Run(target, func(t *testing.T) {
			db := autonomyTestPool(t)
			ctx := context.Background()
			original := autonomyLocalService(t, db, nil, nil)
			noticeID, runID := noticeFixture(t, db, original, "complete", "summary")
			var initial, at time.Time
			if err := db.QueryRow(ctx, `SELECT valid_from,clock_timestamp() FROM telegram_autonomy_identity WHERE user_id=$1`, memory.UserID).Scan(&initial, &at); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `UPDATE autonomy_state SET notice_epoch=$1`, initial); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `UPDATE autonomy_runs SET created_at=$2,completed_at=$3 WHERE id=$1`, runID, initial, at); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `UPDATE autonomy_notices SET created_at=$2 WHERE id=$1`, noticeID, at); err != nil {
				t.Fatal(err)
			}
			cfg := original.cfg
			cfg.AllowedUserID, cfg.AllowedChatID = 789, 789
			next, err := NewWithClient(db, cfg, nil, original.client)
			if err != nil {
				t.Fatal(err)
			}
			if target == "old service" {
				next = original
			}
			if target == "changed back" {
				next, err = NewWithClient(db, original.cfg, nil, original.client)
				if err != nil {
					t.Fatal(err)
				}
			}
			next.now = func() time.Time { return at.Add(time.Minute) }
			if err := next.QueueAutonomyNotice(ctx, noticeID); err != nil {
				t.Fatal(err)
			}
			var count int
			var state string
			if err := db.QueryRow(ctx, `SELECT delivery_state,(SELECT count(*) FROM telegram_outbox) FROM autonomy_notices WHERE id=$1`, noticeID).Scan(&state, &count); err != nil {
				t.Fatal(err)
			}
			if state != "cancelled" || count != 0 {
				t.Fatalf("old artifact retargeted: state=%s outbox=%d", state, count)
			}
		})
	}
}

func TestAutonomyUnchangedIdentityPreservesAnchorAndPendingNotice(t *testing.T) {
	db := autonomyTestPool(t)
	ctx := context.Background()
	s := autonomyLocalService(t, db, nil, nil)
	noticeID, _ := noticeFixture(t, db, s, "complete", "summary")
	var before, after time.Time
	if err := db.QueryRow(ctx, `SELECT valid_from FROM telegram_autonomy_identity WHERE user_id=$1`, memory.UserID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	next, err := NewWithClient(db, s.cfg, nil, s.client)
	if err != nil {
		t.Fatal(err)
	}
	next.now = s.now
	if err := db.QueryRow(ctx, `SELECT valid_from FROM telegram_autonomy_identity WHERE user_id=$1`, memory.UserID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !before.Equal(after) {
		t.Fatalf("unchanged startup reset owner authority: %v -> %v", before, after)
	}
	if err := next.QueueAutonomyNotice(ctx, noticeID); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := db.QueryRow(ctx, `SELECT delivery_state FROM autonomy_notices WHERE id=$1`, noticeID).Scan(&state); err != nil || state != "queued" {
		t.Fatalf("same owner lost pending notice: %s %v", state, err)
	}
}

func TestAutonomyDisabledStillRecoversUnknownClaim(t *testing.T) {
	db := autonomyTestPool(t)
	ctx := context.Background()
	sends := 0
	s := autonomyLocalService(t, db, nil, func(w http.ResponseWriter, r *http.Request) {
		sends++
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":1003}}`)
	})
	noticeID, _ := noticeFixture(t, db, s, "complete", "summary")
	if err := s.QueueAutonomyNotice(ctx, noticeID); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := s.claimAutonomyOne(ctx); err != nil || !claimed {
		t.Fatalf("claim=%v %v", claimed, err)
	}
	if _, err := db.Exec(ctx, `UPDATE telegram_outbox SET claimed_at=clock_timestamp()-interval '2 minutes'`); err != nil {
		t.Fatal(err)
	}
	cfg := s.cfg
	cfg.AutonomyEnabled = false
	next, err := NewWithClient(db, cfg, nil, s.client)
	if err != nil {
		t.Fatal(err)
	}
	next.now = s.now
	if _, err := next.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := db.QueryRow(ctx, `SELECT delivery_state FROM autonomy_notices WHERE id=$1`, noticeID).Scan(&state); err != nil || state != "failed_unknown" || sends != 0 {
		t.Fatalf("disabled recovery state=%s sends=%d err=%v", state, sends, err)
	}
}
