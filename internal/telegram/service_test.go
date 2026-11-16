package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"openchris/conselho/internal/memory"
)

func privateUpdate(id int64, text string) []byte {
	quoted, _ := json.Marshal(text)
	return []byte(fmt.Sprintf(`{"update_id":%d,"message":{"message_id":88,"date":1700000000,"from":{"id":456,"is_bot":false,"first_name":"Synthetic"},"chat":{"id":456,"type":"private"},"text":%s}}`, id, quoted))
}

// The database name is checked before migration or cleanup can write anything.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TELEGRAM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TELEGRAM_TEST_DATABASE_URL is required for isolated Telegram persistence tests")
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil || cfg.ConnConfig.Database != "conselho_telegram_test" {
		t.Fatal("refusing database outside conselho_telegram_test")
	}
	store, err := memory.Open(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	_, err = store.DB.Exec(context.Background(), `TRUNCATE telegram_outbox,telegram_inbox,conversation_events,jobs,episodes,memory_facts,context_manifests,conversation_threads,stream_events RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
	return store.DB
}

func localService(t *testing.T, db *pgxpool.Pool, reply ReplyBuilder, handler http.HandlerFunc) *Service {
	t.Helper()
	if handler == nil {
		handler = func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"ok":true,"result":{"message_id":91}}`) }
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClientWithBaseURL(testConfig().BotToken, server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewWithClient(db, testConfig(), reply, client)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestHandleUpdateRejectsForgedUnsupportedAndInvalidMessages(t *testing.T) {
	s := localService(t, nil, nil, nil)
	ctx := context.Background()
	valid := string(privateUpdate(1, "Olá"))
	if _, err := s.HandleUpdate(ctx, "wrong", []byte(valid)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("secret: %v", err)
	}
	unauthorized := []string{
		strings.Replace(valid, `"from":{"id":456`, `"from":{"id":999`, 1),
		strings.Replace(valid, `"chat":{"id":456`, `"chat":{"id":999`, 1),
		strings.Replace(valid, `"is_bot":false`, `"is_bot":true`, 1),
		strings.Replace(valid, `"type":"private"`, `"type":"group"`, 1),
	}
	for _, raw := range unauthorized {
		if _, err := s.HandleUpdate(ctx, testConfig().WebhookSecret, []byte(raw)); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("forged message accepted: %s %v", raw, err)
		}
	}
	invalid := [][]byte{
		[]byte(`null`), []byte(`{}`), []byte(`[]`), []byte(`{"update_id":1,"message":null}`),
		[]byte(strings.Replace(valid, `"update_id":1`, `"update_id":null`, 1)),
		[]byte(strings.Replace(valid, `"update_id":1`, `"update_id":-1`, 1)),
		[]byte(strings.Replace(valid, `"update_id":1`, `"update_id":1,"update_id":2`, 1)),
		[]byte(strings.Replace(valid, `"is_bot":false,`, "", 1)),
		[]byte(strings.Replace(valid, `"message":`, `"edited_message":`, 1)),
		[]byte(strings.Replace(valid, `"message":`, `"business_message":`, 1)),
		[]byte(strings.Replace(valid, `"text":`, `"forward_origin":{"type":"hidden_user"},"text":`, 1)),
		[]byte(strings.Replace(valid, `"text":`, `"business_connection_id":"foreign","text":`, 1)),
		[]byte(strings.Replace(valid, `"text":`, `"sender_chat":{"id":456},"text":`, 1)),
		privateUpdate(1, ""), privateUpdate(1, "\x00"), privateUpdate(1, strings.Repeat("é", 4097)),
		[]byte(strings.Repeat(" ", 65537)), []byte("{\xff}"), []byte(valid + valid),
	}
	for _, raw := range invalid {
		if _, err := s.HandleUpdate(ctx, testConfig().WebhookSecret, raw); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid accepted: %.200s %v", raw, err)
		}
	}
}

// Removing the transaction, unique constraint or journal insertion breaks these assertions.
func TestAcceptedUpdateDurablyRegistersM0WithoutDeterministicJob(t *testing.T) {
	db := testPool(t)
	ctx := context.Background()
	builds := 0
	s := localService(t, db, func(context.Context, string) (string, error) { builds++; return "Resumo agregado", nil }, nil)
	raw := privateUpdate(10, "Meu CPF é 12345678901 e a conta é confidencial")
	duplicate, err := s.HandleUpdate(ctx, testConfig().WebhookSecret, raw)
	if err != nil || duplicate || builds != 0 {
		t.Fatalf("receipt: %v %v builds=%d", duplicate, err, builds)
	}
	duplicate, err = s.HandleUpdate(ctx, testConfig().WebhookSecret, raw)
	if err != nil || !duplicate {
		t.Fatalf("duplicate: %v %v", duplicate, err)
	}
	if _, err = s.HandleUpdate(ctx, testConfig().WebhookSecret, privateUpdate(10, "changed")); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict: %v", err)
	}
	var inbox, events, jobs, episodes int
	var body, user, state string
	err = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM telegram_inbox),(SELECT count(*) FROM conversation_events),(SELECT count(*) FROM jobs),(SELECT count(*) FROM episodes),e.body,e.user_id::text,t.state FROM conversation_events e JOIN conversation_threads t ON t.last_input_event_id=e.id`).Scan(&inbox, &events, &jobs, &episodes, &body, &user, &state)
	if err != nil || inbox != 1 || events != 1 || jobs != 0 || episodes != 1 || body != "Meu CPF é 12345678901 e a conta é confidencial" || user != memory.UserID || state != "pending" {
		t.Fatalf("M0: inbox=%d events=%d jobs=%d episodes=%d body=%q user=%s state=%s err=%v", inbox, events, jobs, episodes, body, user, state, err)
	}
	// A newly constructed service consumes the accepted message after restart.
	next, err := NewWithClient(db, testConfig(), s.reply, s.client)
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := next.ProcessOne(ctx); err != nil || !processed {
		t.Fatalf("restart process: %v %v", processed, err)
	}
	if builds != 0 {
		t.Fatal("free text reached reply builder")
	}
	var output string
	if err = db.QueryRow(ctx, `SELECT body FROM conversation_events WHERE direction='outgoing'`).Scan(&output); err != nil || strings.Contains(output, "12345678901") || !strings.Contains(output, "IA não configurada") {
		t.Fatalf("unsafe response %q %v", output, err)
	}
}

func TestIngressRollbackLeavesNoPartialReceipt(t *testing.T) {
	db := testPool(t)
	ctx := context.Background()
	s := localService(t, db, nil, nil)
	_, err := db.Exec(ctx, `CREATE FUNCTION telegram_test_fail() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RAISE EXCEPTION ''local failure''; END'; CREATE TRIGGER telegram_test_fail BEFORE INSERT ON stream_events FOR EACH ROW EXECUTE FUNCTION telegram_test_fail()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(context.Background(), `DROP TRIGGER IF EXISTS telegram_test_fail ON stream_events; DROP FUNCTION IF EXISTS telegram_test_fail()`)
	})
	if _, err = s.HandleUpdate(ctx, testConfig().WebhookSecret, privateUpdate(11, "Olá")); err == nil {
		t.Fatal("injected journal failure ignored")
	}
	var count int
	if err = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM telegram_inbox)+(SELECT count(*) FROM conversation_events)+(SELECT count(*) FROM episodes)`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial transaction: %d %v", count, err)
	}
}

func TestConcurrentWorkersSendOnePersistedCommandResponse(t *testing.T) {
	db := testPool(t)
	ctx := context.Background()
	var sends, builds atomic.Int32
	s := localService(t, db, func(ctx context.Context, command string) (string, error) {
		if command != "/resumo" {
			t.Errorf("raw text leaked: %q", command)
		}
		builds.Add(1)
		return "Resumo agregado", nil
	}, func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":92}}`)
	})
	if _, err := s.HandleUpdate(ctx, testConfig().WebhookSecret, privateUpdate(12, "/resumo detalhes privados")); err != nil {
		t.Fatal(err)
	}
	next, _ := NewWithClient(db, testConfig(), s.reply, s.client)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, worker := range []*Service{s, next} {
		wg.Add(1)
		go func(worker *Service) { defer wg.Done(); _, err := worker.ProcessOne(ctx); errs <- err }(worker)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if sends.Load() != 1 || builds.Load() != 1 {
		t.Fatalf("duplicate work: sends=%d builds=%d", sends.Load(), builds.Load())
	}
	var outgoing, complete int
	var state string
	err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM conversation_events WHERE direction='outgoing'),(SELECT count(*) FROM telegram_outbox WHERE status='sent'),(SELECT state FROM conversation_threads WHERE channel='telegram')`).Scan(&outgoing, &complete, &state)
	if err != nil || outgoing != 1 || complete != 1 || state != "complete" {
		t.Fatalf("missing result: %d %d %s %v", outgoing, complete, state, err)
	}
}

func TestOutboxRetries429FiveTimesAndHonorsRetryAfter(t *testing.T) {
	db := testPool(t)
	ctx := context.Background()
	sends := 0
	s := localService(t, db, nil, func(w http.ResponseWriter, r *http.Request) {
		sends++
		w.WriteHeader(429)
		fmt.Fprint(w, `{"ok":false,"error_code":429,"parameters":{"retry_after":30}}`)
	})
	if _, err := s.HandleUpdate(ctx, testConfig().WebhookSecret, privateUpdate(13, "/start")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if processed, err := s.ProcessOne(ctx); err != nil || !processed {
			t.Fatalf("attempt %d: %v %v", i, processed, err)
		}
		var status string
		var attempts int
		var delay float64
		if err := db.QueryRow(ctx, `SELECT status,attempts,extract(epoch from next_attempt_at-clock_timestamp())::float8 FROM telegram_outbox`).Scan(&status, &attempts, &delay); err != nil {
			t.Fatal(err)
		}
		if attempts != i+1 || (i < 4 && (status != "pending" || delay < 28)) || (i == 4 && status != "failed") {
			t.Fatalf("retry state %s %d delay=%f", status, attempts, delay)
		}
		if i == 0 {
			overview, err := s.Status(ctx)
			if err != nil || overview["pending"] != int64(1) || overview["queued"] != int64(1) {
				t.Fatalf("queued retry missing from pending total: %v %v", overview, err)
			}
		}
		if processed, err := s.ProcessOne(ctx); err != nil || processed {
			t.Fatalf("retry ignored schedule: %v %v", processed, err)
		}
		if _, err := db.Exec(ctx, `UPDATE telegram_outbox SET next_attempt_at=now()`); err != nil {
			t.Fatal(err)
		}
	}
	if sends != 5 {
		t.Fatalf("unbounded sends %d", sends)
	}
}

func TestAmbiguousSendIsNeverAutomaticallyReplayed(t *testing.T) {
	testAmbiguousSend(t, false)
}

func TestAmbiguousSendWaitsForInitialEligibilityWithoutReplaying(t *testing.T) {
	testAmbiguousSend(t, true)
}

func processUntilUnknown(t *testing.T, s *Service, ctx context.Context, updateID int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := s.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
		var state string
		if err := s.db.QueryRow(ctx, `SELECT COALESCE((SELECT status FROM telegram_outbox WHERE bot_id=$1 AND update_id=$2),'')`, s.botID, updateID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "unknown" {
			return
		}
		if state == "sent" || state == "failed" {
			t.Fatalf("ambiguous delivery reached wrong terminal state: %s", state)
		}
		if time.Now().After(deadline) {
			var inboxState string
			var eligibleAt, databaseAt time.Time
			diagnosticErr := s.db.QueryRow(ctx, `SELECT status,next_run_at,clock_timestamp() FROM telegram_inbox WHERE bot_id=$1 AND update_id=$2`, s.botID, updateID).Scan(&inboxState, &eligibleAt, &databaseAt)
			t.Fatalf("ambiguous delivery did not finish: outbox=%s inbox=%s eligible_at=%s db_at=%s future_by=%s err=%v", state, inboxState, eligibleAt.Format(time.RFC3339Nano), databaseAt.Format(time.RFC3339Nano), eligibleAt.Sub(databaseAt), diagnosticErr)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func testAmbiguousSend(t *testing.T, initialTimestampAhead bool) {
	t.Helper()
	db := testPool(t)
	ctx := context.Background()
	sends := 0
	s := localService(t, db, nil, func(w http.ResponseWriter, r *http.Request) {
		sends++
		connection, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			connection.Close()
		}
	})
	if _, err := s.HandleUpdate(ctx, testConfig().WebhookSecret, privateUpdate(14, "/ajuda")); err != nil {
		t.Fatal(err)
	}
	if initialTimestampAhead {
		// A future timestamp must remain ineligible. Then simulate the clock
		// catching up, retaining a small offset across the first worker cycles.
		if _, err := db.Exec(ctx, `UPDATE telegram_inbox SET next_run_at=clock_timestamp()+interval '5 minutes' WHERE bot_id=$1 AND update_id=14`, s.botID); err != nil {
			t.Fatal(err)
		}
		if processed, err := s.ProcessOne(ctx); err != nil || processed || sends != 0 {
			t.Fatalf("future inbox bypassed eligibility: processed=%v sends=%d err=%v", processed, sends, err)
		}
		if _, err := db.Exec(ctx, `UPDATE telegram_inbox SET next_run_at=clock_timestamp()+interval '20 milliseconds' WHERE bot_id=$1 AND update_id=14`, s.botID); err != nil {
			t.Fatal(err)
		}
	}
	processUntilUnknown(t, s, ctx, 14)
	next, _ := NewWithClient(db, testConfig(), nil, s.client)
	if _, err := next.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	var outgoing int
	if err := db.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM conversation_events WHERE direction='outgoing') FROM telegram_outbox`).Scan(&status, &outgoing); err != nil || status != "unknown" || outgoing != 0 || sends != 1 {
		t.Fatalf("ambiguous replay: status=%s outgoing=%d sends=%d err=%v", status, outgoing, sends, err)
	}
}

func TestKnownProviderFailureRetriesStoredReplyAndCrashClaimBecomesUnknown(t *testing.T) {
	db := testPool(t)
	ctx := context.Background()
	sends, builds := 0, 0
	s := localService(t, db, func(context.Context, string) (string, error) { builds++; return "Totais agregados", nil }, func(w http.ResponseWriter, r *http.Request) {
		sends++
		if sends == 1 {
			w.WriteHeader(503)
			fmt.Fprint(w, `{"ok":false,"error_code":503}`)
			return
		}
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":95}}`)
	})
	if _, err := s.HandleUpdate(ctx, testConfig().WebhookSecret, privateUpdate(15, "/gastos")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE telegram_outbox SET next_attempt_at=now()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if sends != 2 || builds != 1 {
		t.Fatalf("reply changed on retry: sends=%d builds=%d", sends, builds)
	}
	if _, err := s.HandleUpdate(ctx, testConfig().WebhookSecret, privateUpdate(16, "/start")); err != nil {
		t.Fatal(err)
	}
	if prepared, err := s.prepareOne(ctx); err != nil || !prepared {
		t.Fatalf("prepare before crash: %v %v", prepared, err)
	}
	if _, claimed, err := s.claimOne(ctx); err != nil || !claimed {
		t.Fatalf("claim before crash: %v %v", claimed, err)
	}
	// Crash after the durable claim and before observing the provider response.
	if _, err := db.Exec(ctx, `UPDATE telegram_outbox SET claimed_at=clock_timestamp()-interval '2 minutes' WHERE status='sending'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	var unknown int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM telegram_outbox WHERE status='unknown'`).Scan(&unknown); err != nil || unknown != 1 {
		t.Fatalf("crash claim replayed: %d %v", unknown, err)
	}
	if sends != 2 {
		t.Fatalf("crash claim was sent again: %d", sends)
	}
}

func TestReplyBuilderFailureIsBoundedAndNeverSendsRawError(t *testing.T) {
	db := testPool(t)
	ctx := context.Background()
	builds, sends := 0, 0
	s := localService(t, db, func(context.Context, string) (string, error) {
		builds++
		return "", errors.New("private CPF 12345678901")
	}, func(w http.ResponseWriter, r *http.Request) {
		sends++
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":96}}`)
	})
	if _, err := s.HandleUpdate(ctx, testConfig().WebhookSecret, privateUpdate(19, "/memoria")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := s.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `UPDATE telegram_inbox SET next_run_at=now()`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	status, err := s.Status(ctx)
	if err != nil || builds != 5 || sends != 0 || status["status"] != "failed" || status["failed"] != int64(1) {
		t.Fatalf("builder failure: status=%v builds=%d sends=%d err=%v", status, builds, sends, err)
	}
	var body, errorCode string
	if err = db.QueryRow(ctx, `SELECT e.body,i.error_code FROM telegram_inbox i JOIN conversation_events e ON e.id=i.incoming_event_id`).Scan(&body, &errorCode); err != nil || body != "/memoria" || errorCode != "reply_unavailable" {
		t.Fatalf("raw error leaked or M0 lost: %q %q %v", body, errorCode, err)
	}
}

func TestRunDrainsQueueAndStatusExposesOnlyOperationalMetadata(t *testing.T) {
	db := testPool(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := localService(t, db, nil, nil)
	if _, err := s.HandleUpdate(ctx, testConfig().WebhookSecret, privateUpdate(17, "private text")); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.Run(ctx, func(err error) { t.Errorf("worker error %v", err) }); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		status, err := s.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(status)
		for _, secret := range []string{testConfig().BotToken, testConfig().WebhookSecret, "private text", "456"} {
			if strings.Contains(string(encoded), secret) {
				t.Fatalf("status leak: %s", encoded)
			}
		}
		if status["sent"] == int64(1) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue not drained: %s", encoded)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker ignores cancellation")
	}
}

func TestDisabledServiceNeedsNoDatabaseOrCredentials(t *testing.T) {
	s, err := New(nil, Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.HandleUpdate(context.Background(), "", nil); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled ingress: %v", err)
	}
	if err = s.Setup(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled setup: %v", err)
	}
	if _, err = s.ProcessOne(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled worker: %v", err)
	}
	status, err := s.Status(context.Background())
	if err != nil || status["enabled"] != false {
		t.Fatalf("disabled status: %v %v", status, err)
	}
}

func TestStatusReportsObservedConnectionAndTimestamps(t *testing.T) {
	db := testPool(t)
	ctx := context.Background()
	s := localService(t, db, nil, nil)
	status, err := s.Status(ctx)
	if err != nil || status["status"] != "waiting_setup" || status["allowed_identity_configured"] != true || status["webhook_url"] != "https://example.test/api/telegram/webhook" || status["last_received_at"] != nil || status["last_sent_at"] != nil {
		t.Fatalf("unobserved status: %v %v", status, err)
	}
	if _, err = s.HandleUpdate(ctx, testConfig().WebhookSecret, privateUpdate(18, "/start")); err != nil {
		t.Fatal(err)
	}
	status, err = s.Status(ctx)
	if err != nil || status["status"] != "pending" || status["last_received_at"] == nil {
		t.Fatalf("receipt not observed: %v %v", status, err)
	}
	if _, err = s.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	status, err = s.Status(ctx)
	if err != nil || status["status"] != "connected" || status["last_sent_at"] == nil {
		t.Fatalf("send not observed: %v %v", status, err)
	}
}

func TestAllowlistChangeRevokesQueuedAndUnpreparedUpdates(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared_%t", prepared), func(t *testing.T) {
			db := testPool(t)
			ctx := context.Background()
			sends, builds := 0, 0
			rateLimit := prepared
			first := localService(t, db, func(context.Context, string) (string, error) { builds++; return "Totais agregados", nil }, func(w http.ResponseWriter, r *http.Request) {
				sends++
				if rateLimit {
					w.WriteHeader(429)
					fmt.Fprint(w, `{"ok":false,"error_code":429,"parameters":{"retry_after":30}}`)
					return
				}
				fmt.Fprint(w, `{"ok":true,"result":{"message_id":98}}`)
			})
			if _, err := first.HandleUpdate(ctx, testConfig().WebhookSecret, privateUpdate(30, "/resumo")); err != nil {
				t.Fatal(err)
			}
			if prepared {
				if _, err := first.ProcessOne(ctx); err != nil {
					t.Fatal(err)
				}
			}
			initialSends, initialBuilds := sends, builds
			rateLimit = false
			cfg := testConfig()
			cfg.AllowedUserID = 789
			cfg.AllowedChatID = 789
			next, err := NewWithClient(db, cfg, first.reply, first.client)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(ctx, `UPDATE telegram_outbox SET next_attempt_at=now()`); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if _, err = next.ProcessOne(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if sends != initialSends || builds != initialBuilds {
				t.Fatalf("revoked identity triggered work: sends=%d want=%d builds=%d want=%d", sends, initialSends, builds, initialBuilds)
			}
			var status, errorCode string
			var journal int
			if err = db.QueryRow(ctx, `SELECT status,error_code,(SELECT count(*) FROM conversation_events WHERE direction='incoming') FROM telegram_inbox WHERE update_id=30`).Scan(&status, &errorCode, &journal); err != nil || status != "failed" || errorCode != "authorization_revoked" || journal != 1 {
				t.Fatalf("revocation lost audit: %s %s journal=%d %v", status, errorCode, journal, err)
			}
			if prepared {
				if err = db.QueryRow(ctx, `SELECT status,error_code FROM telegram_outbox WHERE update_id=30`).Scan(&status, &errorCode); err != nil || status != "failed" || errorCode != "authorization_revoked" {
					t.Fatalf("outbox not revoked: %s %s %v", status, errorCode, err)
				}
			}
			// Only a fresh incoming update may create output for the new identity.
			raw := []byte(strings.ReplaceAll(string(privateUpdate(31, "/resumo")), "456", "789"))
			if _, err = next.HandleUpdate(ctx, cfg.WebhookSecret, raw); err != nil {
				t.Fatal(err)
			}
			if _, err = next.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			if sends != initialSends+1 || builds != initialBuilds+1 {
				t.Fatalf("new identity blocked: sends=%d builds=%d", sends, builds)
			}
		})
	}
}

func TestTamperedOutboxDestinationIsRevokedBeforeHTTP(t *testing.T) {
	db := testPool(t)
	ctx := context.Background()
	sends := 0
	s := localService(t, db, nil, func(w http.ResponseWriter, r *http.Request) {
		sends++
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":99}}`)
	})
	if _, err := s.HandleUpdate(ctx, testConfig().WebhookSecret, privateUpdate(32, "/start")); err != nil {
		t.Fatal(err)
	}
	if prepared, err := s.prepareOne(ctx); err != nil || !prepared {
		t.Fatalf("prepare: %v %v", prepared, err)
	}
	if _, err := db.Exec(ctx, `UPDATE telegram_outbox SET chat_id=999`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	var status, errorCode string
	if err := db.QueryRow(ctx, `SELECT status,error_code FROM telegram_outbox`).Scan(&status, &errorCode); err != nil || status != "failed" || errorCode != "authorization_revoked" || sends != 0 {
		t.Fatalf("tampered destination sent: status=%s code=%s sends=%d err=%v", status, errorCode, sends, err)
	}
}

func TestHistoricalReceiptsWithoutProvenIdentityNeverSend(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared_%t", prepared), func(t *testing.T) {
			db := testPool(t)
			ctx := context.Background()
			sends, builds := 0, 0
			s := localService(t, db, func(context.Context, string) (string, error) { builds++; return "Totais agregados", nil }, func(w http.ResponseWriter, r *http.Request) {
				sends++
				fmt.Fprint(w, `{"ok":true,"result":{"message_id":100}}`)
			})
			if _, err := s.HandleUpdate(ctx, testConfig().WebhookSecret, privateUpdate(33, "/resumo")); err != nil {
				t.Fatal(err)
			}
			if prepared {
				if _, err := s.prepareOne(ctx); err != nil {
					t.Fatal(err)
				}
			}
			initialBuilds := builds
			if _, err := db.Exec(ctx, `UPDATE telegram_inbox SET accepted_user_id=NULL,accepted_chat_id=NULL`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			var status, errorCode string
			if err := db.QueryRow(ctx, `SELECT status,error_code FROM telegram_inbox`).Scan(&status, &errorCode); err != nil || status != "failed" || errorCode != "authorization_revoked" || sends != 0 || builds != initialBuilds {
				t.Fatalf("historical identity inferred: %s %s sends=%d builds=%d err=%v", status, errorCode, sends, builds, err)
			}
		})
	}
}

func TestHTTPBoundaryRechecksIdentityOfAlreadyClaimedDelivery(t *testing.T) {
	db := testPool(t)
	ctx := context.Background()
	sends := 0
	first := localService(t, db, nil, func(w http.ResponseWriter, r *http.Request) {
		sends++
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":101}}`)
	})
	if _, err := first.HandleUpdate(ctx, testConfig().WebhookSecret, privateUpdate(34, "/start")); err != nil {
		t.Fatal(err)
	}
	if _, err := first.prepareOne(ctx); err != nil {
		t.Fatal(err)
	}
	item, claimed, err := first.claimOne(ctx)
	if err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	cfg := testConfig()
	cfg.AllowedUserID = 789
	cfg.AllowedChatID = 789
	next, err := NewWithClient(db, cfg, nil, first.client)
	if err != nil {
		t.Fatal(err)
	}
	if err = next.sendClaimed(ctx, item); err != nil {
		t.Fatal(err)
	}
	var status, errorCode string
	if err = db.QueryRow(ctx, `SELECT status,error_code FROM telegram_outbox`).Scan(&status, &errorCode); err != nil || status != "failed" || errorCode != "authorization_revoked" || sends != 0 {
		t.Fatalf("HTTP boundary trusted revoked claim: %s %s sends=%d err=%v", status, errorCode, sends, err)
	}
}
