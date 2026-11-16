package pluggy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"openchris/conselho/internal/memory"
	"openchris/conselho/migrations"
)

const testWebhookSecret = "0123456789abcdef0123456789abcdef"

func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("PLUGGY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PLUGGY_TEST_DATABASE_URL not set; database tests require conselho_pluggy_test")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || cfg.ConnConfig.Database != "conselho_pluggy_test" {
		t.Fatal("refusing Pluggy test setup outside conselho_pluggy_test")
	}
	db, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	// Guard above is required before applying even a migration, let alone cleanup.
	for _, file := range []string{"001_memory.sql", "004_pluggy.sql", "010_pluggy_wealth.sql"} {
		sql, e := migrations.Files.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(context.Background(), string(sql)); e != nil {
			t.Fatal(e)
		}
	}
	if _, err = db.Exec(context.Background(), `TRUNCATE pluggy_jobs,pluggy_inbox,pluggy_audit,pluggy_snapshots,pluggy_transactions,pluggy_investments,pluggy_accounts,pluggy_items RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	return db
}

type fixture struct {
	mode     atomic.Int32
	requests atomic.Int32
	owner    string
	itemID   string
}

func (f *fixture) handler(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	if r.URL.Path == "/auth" {
		fmt.Fprint(w, `{"apiKey":"secret-key"}`)
		return
	}
	if r.Header.Get("X-API-KEY") != "secret-key" {
		http.Error(w, "wrong key", 401)
		return
	}
	switch r.URL.Path {
	case "/items/" + f.itemID:
		if f.mode.Load() == 3 {
			http.Error(w, "gone", 404)
			return
		}
		expires := "2099-01-01T00:00:00Z"
		if f.mode.Load() == 2 {
			expires = "2020-01-01T00:00:00Z"
		}
		fmt.Fprintf(w, `{"id":%q,"clientUserId":%q,"connector":{"id":200,"name":"MeuPluggy","credentials":[{"value":"must-not-store"}]},"status":"UPDATED","executionStatus":"SUCCESS","consentExpiresAt":%q,"lastUpdatedAt":"2026-10-20T12:00:00Z","error":null,"products":["ACCOUNTS","TRANSACTIONS","INVESTMENTS"]}`, f.itemID, f.owner, expires)
	case "/accounts":
		if f.mode.Load() == 4 || f.mode.Load() == 7 {
			fmt.Fprint(w, `{"totalPages":1,"results":[]}`)
			return
		}
		fmt.Fprint(w, `{"page":1,"totalPages":1,"results":[{"id":"`+testAccount+`","itemId":"`+f.itemID+`","name":"Conta em dólar","type":"BANK","balance":9007199254740993.123456789,"currencyCode":"USD","updatedAt":null}]}`)
	case "/v2/transactions":
		if f.mode.Load() == 8 {
			http.Error(w, "product endpoint gone", http.StatusGone)
			return
		}
		if f.mode.Load() == 6 {
			fmt.Fprint(w, `{"next":null,"results":[]}`)
			return
		}
		if f.mode.Load() == 1 && r.URL.Query().Get("after") == "second" {
			http.Error(w, "temporary provider failure", 503)
			return
		}
		next := "null"
		if f.mode.Load() == 1 {
			next = fmt.Sprintf("%q", "?accountId="+testAccount+"&after=second")
		}
		fmt.Fprintf(w, `{"next":%s,"results":[{"id":"transaction-1","accountId":"`+testAccount+`","amount":-0.123456789123456789,"currencyCode":"USD","date":"2026-10-19T12:00:00.123Z","description":"Café","status":"POSTED"}]}`, next)
	case "/investments":
		if f.mode.Load() == 4 || f.mode.Load() == 5 {
			fmt.Fprint(w, `{"totalPages":1,"results":[]}`)
			return
		}
		fmt.Fprint(w, `{"page":1,"totalPages":1,"results":[{"id":"investment-1","name":"Posição","balance":123.000000000000000001,"quantity":1.000000000000000001,"unitValue":null,"currencyCode":"USD","date":null}]}`)
	default:
		http.NotFound(w, r)
	}
}
func testService(t *testing.T, db *pgxpool.Pool, f *fixture) *Service {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(server.Close)
	s, err := New(db, Config{Enabled: true, ClientID: "id", ClientSecret: "server-secret", WebhookSecret: testWebhookSecret, PublicURL: "https://conselho.example/webhooks/pluggy", APIBaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Routing a read-only status to the API or enabling a half-configured connector breaks this test.
func TestDisabledServiceDoesNotCallAPIAndRejectsIncompleteConfig(t *testing.T) {
	s, err := New(nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConnectToken(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	status, err := s.Status(context.Background())
	if err != nil || status["enabled"] != false {
		t.Fatalf("disabled status: %v %v", status, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.Run(ctx, func(error) { t.Error("disabled worker ran") })
	if _, err = New(nil, Config{Enabled: true, ClientID: "id"}); err == nil {
		t.Fatal("incomplete config accepted")
	}
}

func TestIngestionStoresExactSourceEvidenceAndStableVersions(t *testing.T) {
	db := testDB(t)
	f := &fixture{owner: memory.UserID, itemID: testItem}
	s := testService(t, db, f)
	ctx := context.Background()
	if err := s.RegisterItem(ctx, testItem); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncItem(ctx, testItem); err != nil {
		t.Fatal(err)
	}
	var balance, currency string
	var sourceTime *time.Time
	var version int
	if err := db.QueryRow(ctx, `SELECT balance::text,currency,source_updated_at,version FROM pluggy_accounts WHERE external_id=$1`, testAccount).Scan(&balance, &currency, &sourceTime, &version); err != nil {
		t.Fatal(err)
	}
	if balance != "9007199254740993.123456789" || currency != "USD" || sourceTime != nil || version != 1 {
		t.Fatalf("source changed: %s %s %v v%d", balance, currency, sourceTime, version)
	}
	var amount string
	if err := db.QueryRow(ctx, `SELECT amount::text FROM pluggy_transactions WHERE external_id='transaction-1'`).Scan(&amount); err != nil || amount != "-0.123456789123456789" {
		t.Fatalf("decimal drift: %s %v", amount, err)
	}
	if err := s.SyncItem(ctx, testItem); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT version FROM pluggy_accounts WHERE external_id=$1`, testAccount).Scan(&version); err != nil || version != 1 {
		t.Fatalf("unchanged observation changed version: %d %v", version, err)
	}
	before := f.requests.Load()
	status, err := s.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	data, err := s.Data(ctx)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(data)
	if status["enabled"] != true || strings.Contains(string(encoded), "server-secret") || strings.Contains(string(encoded), "must-not-store") || f.requests.Load() != before {
		t.Fatalf("unsafe read-only view: %s", encoded)
	}
	if len(data["accounts"].([]Object)) != 1 || len(data["transactions"].([]Object)) != 1 || len(data["investments"].([]Object)) != 1 {
		t.Fatal("missing stored products")
	}
}

// A failed later page, empty expired authorization, or revoked item must not erase last good data.
func TestIncompleteExpiredRevokedAndEmptyReadsPreserveLastSnapshot(t *testing.T) {
	for _, mode := range []int32{1, 2, 3, 4, 8} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			db := testDB(t)
			f := &fixture{owner: memory.UserID, itemID: testItem}
			s := testService(t, db, f)
			ctx := context.Background()
			if err := s.RegisterItem(ctx, testItem); err != nil {
				t.Fatal(err)
			}
			if err := s.SyncItem(ctx, testItem); err != nil {
				t.Fatal(err)
			}
			var old time.Time
			if err := db.QueryRow(ctx, `SELECT last_synced_at FROM pluggy_items WHERE item_id=$1`, testItem).Scan(&old); err != nil {
				t.Fatal(err)
			}
			f.mode.Store(mode)
			_ = s.SyncItem(ctx, testItem)
			var current time.Time
			var state, balance string
			if err := db.QueryRow(ctx, `SELECT status,last_synced_at FROM pluggy_items WHERE item_id=$1`, testItem).Scan(&state, &current); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(ctx, `SELECT balance::text FROM pluggy_accounts WHERE external_id=$1`, testAccount).Scan(&balance); err != nil {
				t.Fatal(err)
			}
			want := map[int32]string{1: "partial", 2: "expired", 3: "revoked", 4: "partial", 8: "partial"}[mode]
			if state != want || !old.Equal(current) || balance != "9007199254740993.123456789" {
				t.Fatalf("lost last good snapshot: %s %s %s", state, current, balance)
			}
		})
	}
}

func TestWebhookAuthenticatesDeduplicatesAndDefersDiscovery(t *testing.T) {
	db := testDB(t)
	f := &fixture{owner: memory.UserID, itemID: testItem}
	s := testService(t, db, f)
	ctx := context.Background()
	event := []byte(`{"event":"item/created","eventId":"d876fd7c-e9bd-4c4c-bd46-cc96c62aac29","itemId":"` + testItem + `","clientUserId":"` + memory.UserID + `"}`)
	if _, err := s.HandleWebhook(ctx, "wrong", event); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("wrong webhook secret accepted")
	}
	if _, err := s.HandleWebhook(ctx, testWebhookSecret, []byte(`{"event":"item/created","itemId":"`+testItem+`"}`)); err == nil {
		t.Fatal("missing event ID accepted")
	}
	if duplicate, err := s.HandleWebhook(ctx, testWebhookSecret, event); err != nil || duplicate {
		t.Fatalf("first delivery: %v %v", duplicate, err)
	}
	if duplicate, err := s.HandleWebhook(ctx, testWebhookSecret, event); err != nil || !duplicate {
		t.Fatalf("duplicate delivery: %v %v", duplicate, err)
	}
	if f.requests.Load() != 0 {
		t.Fatal("webhook did synchronous provider work")
	}
	var jobs int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM pluggy_jobs`).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("duplicate jobs: %d %v", jobs, err)
	}
	if did, err := s.processOne(ctx); err != nil || !did {
		t.Fatalf("durable worker: %v %v", did, err)
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM pluggy_accounts`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("discovery not ingested: %d %v", n, err)
	}
	// Unknown transaction/item-updated events cannot discover another owner's item.
	unknown := strings.ReplaceAll(string(event), testItem, "00000000-0000-4000-8000-000000000099")
	unknown = strings.ReplaceAll(unknown, "d876fd7c-e9bd-4c4c-bd46-cc96c62aac29", "d876fd7c-e9bd-4c4c-bd46-cc96c62aac28")
	unknown = strings.ReplaceAll(unknown, "item/created", "transactions/created")
	before := f.requests.Load()
	if _, err := s.HandleWebhook(ctx, testWebhookSecret, []byte(unknown)); err != nil {
		t.Fatal(err)
	}
	if did, err := s.processOne(ctx); err != nil || did || before != f.requests.Load() {
		t.Fatalf("unknown item was discovered: %v %v", did, err)
	}
}

func TestDiscoveryRejectsForeignOwnerAndExplicitLegacyImportIsAllowed(t *testing.T) {
	db := testDB(t)
	f := &fixture{owner: "other-owner", itemID: testItem}
	s := testService(t, db, f)
	ctx := context.Background()
	if err := s.RegisterItem(ctx, testItem); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("foreign owner accepted: %v", err)
	}
	f.owner = ""
	if err := s.register(ctx, testItem, false); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("legacy webhook discovery accepted: %v", err)
	}
	if err := s.RegisterItem(ctx, testItem); err != nil {
		t.Fatalf("explicit owner legacy import refused: %v", err)
	}
}

func TestWebhookSetupOnlySubscribesToFinancialEventsAndIsIdempotent(t *testing.T) {
	saved := []Object{}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/auth" {
			fmt.Fprint(w, `{"apiKey":"key"}`)
			return
		}
		if r.URL.Path != "/webhooks" {
			t.Fatalf("unexpected %s", r.URL)
		}
		if r.Method == "GET" {
			_ = json.NewEncoder(w).Encode(Object{"results": saved, "totalPages": 1})
			return
		}
		var body Object
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Fatal("invalid webhook body")
		}
		headers, _ := body["headers"].(map[string]any)
		if headers[WebhookHeader] != testWebhookSecret || body["event"] == "all" || body["url"] != "https://conselho.example/webhooks/pluggy" {
			t.Fatalf("unsafe subscription: %v", body)
		}
		saved = append(saved, body)
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer server.Close()
	s, err := New(nil, Config{Enabled: true, ClientID: "id", ClientSecret: "server-secret", WebhookSecret: testWebhookSecret, PublicURL: "https://conselho.example/webhooks/pluggy", APIBaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetupWebhooks(context.Background()); err != nil {
		t.Fatal(err)
	}
	count := len(saved)
	if count != 10 {
		t.Fatalf("financial event coverage: %d", count)
	}
	if err = s.SetupWebhooks(context.Background()); err != nil || len(saved) != count {
		t.Fatalf("duplicate subscriptions: %d %v", len(saved), err)
	}
}

func TestEnabledConfigRequiresStrongWebhookSecret(t *testing.T) {
	cfg := Config{Enabled: true, ClientID: "id", ClientSecret: "server-secret", WebhookSecret: "short", PublicURL: "https://conselho.example/webhooks/pluggy"}
	if cfg.Validate() == nil {
		t.Fatal("short webhook secret accepted")
	}
}

func TestItemMetadataRemovesNestedCredentialFields(t *testing.T) {
	item := Object{"id": testItem, "statusDetail": Object{"accounts": Object{"status": "UPDATED", "token": "do-not-persist", "credentials": Object{"password": "do-not-persist"}}}}
	stored, _ := json.Marshal(safeItem(item))
	if strings.Contains(string(stored), "do-not-persist") {
		t.Fatalf("unsafe stored metadata: %s", stored)
	}
}

func TestRenewedItemRetainsOneIdentityAndImmutableSourceHistory(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	f := &fixture{owner: memory.UserID, itemID: testItem}
	s := testService(t, db, f)
	if err := s.RegisterItem(ctx, testItem); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncItem(ctx, testItem); err != nil {
		t.Fatal(err)
	}
	newID := "00000000-0000-4000-8000-000000000088"
	f.itemID = newID
	if err := s.RegisterItem(ctx, newID); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncItem(ctx, newID); err != nil {
		t.Fatal(err)
	}
	var accounts, investments int
	var state string
	if err := db.QueryRow(ctx, `SELECT count(*) FROM pluggy_accounts`).Scan(&accounts); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM pluggy_investments`).Scan(&investments); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT status FROM pluggy_items WHERE item_id=$1`, testItem).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if accounts != 1 || investments != 1 || state != "revoked" {
		t.Fatalf("duplicate source identity: %d %d %s", accounts, investments, state)
	}
	var oldHistory, newHistory int
	if err := db.QueryRow(ctx, `SELECT count(*) FILTER(WHERE item_id=$1),count(*) FILTER(WHERE item_id=$2) FROM pluggy_snapshots WHERE kind='investment'`, testItem, newID).Scan(&oldHistory, &newHistory); err != nil {
		t.Fatal(err)
	}
	if oldHistory != 1 || newHistory != 1 {
		t.Fatalf("source history lost at renewal: old=%d new=%d", oldHistory, newHistory)
	}
}

func TestExpiredCachedItemDoesNotConsumeAConnectionSlot(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	f := &fixture{owner: memory.UserID, itemID: testItem}
	s := testService(t, db, f)
	for index := 1; index <= 5; index++ {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", index)
		expires := "2099-01-01T00:00:00Z"
		if index == 1 {
			expires = "2020-01-01T00:00:00Z"
		}
		if _, err := db.Exec(ctx, `INSERT INTO pluggy_items(item_id,user_id,status,last_synced_at,consent_expires_at) VALUES($1,$2,'connected',clock_timestamp(),$3::timestamptz)`, id, memory.UserID, expires); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RegisterItem(ctx, testItem); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncItem(ctx, testItem); err != nil {
		t.Fatalf("expired authorization blocked renewal: %v", err)
	}
}

func TestDurableFailedJobBacksOffAndCompletesAfterRestart(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	f := &fixture{owner: memory.UserID, itemID: testItem}
	s := testService(t, db, f)
	if err := s.RegisterItem(ctx, testItem); err != nil {
		t.Fatal(err)
	}
	f.mode.Store(1)
	if did, err := s.processOne(ctx); !did || err == nil {
		t.Fatalf("failed provider request did not back off: %v %v", did, err)
	}
	var state string
	var attempts int
	if err := db.QueryRow(ctx, `SELECT status,attempts FROM pluggy_jobs`).Scan(&state, &attempts); err != nil || state != "pending" || attempts != 1 {
		t.Fatalf("lost durable retry: %s %d %v", state, attempts, err)
	}
	if did, err := s.processOne(ctx); did || err != nil {
		t.Fatalf("ignored backoff: %v %v", did, err)
	}
	if _, err := db.Exec(ctx, `UPDATE pluggy_jobs SET available_at=clock_timestamp()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	f.mode.Store(0)
	restarted := testService(t, db, f)
	if did, err := restarted.processOne(ctx); !did || err != nil {
		t.Fatalf("restart did not resume job: %v %v", did, err)
	}
	if err := db.QueryRow(ctx, `SELECT status,attempts FROM pluggy_jobs`).Scan(&state, &attempts); err != nil || state != "complete" || attempts != 2 {
		t.Fatalf("job did not complete: %s %d %v", state, attempts, err)
	}
}

func TestStoredConsentExpiryAppearsWithoutProviderRequests(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	f := &fixture{owner: memory.UserID, itemID: testItem}
	s := testService(t, db, f)
	if err := s.RegisterItem(ctx, testItem); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncItem(ctx, testItem); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE pluggy_items SET consent_expires_at=clock_timestamp()-interval '1 second' WHERE item_id=$1`, testItem); err != nil {
		t.Fatal(err)
	}
	before := f.requests.Load()
	status, err := s.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	items := status["items"].([]Object)
	if status["status"] != "expired" || items[0]["status"] != "expired" || f.requests.Load() != before {
		t.Fatalf("expired consent hidden by cached UPDATED: %v", status)
	}
}

func TestEmptyPreviouslyObservedProductPreservesEveryActiveSourceSnapshot(t *testing.T) {
	for _, mode := range []int32{5, 6, 7} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			db := testDB(t)
			ctx := context.Background()
			f := &fixture{owner: memory.UserID, itemID: testItem}
			s := testService(t, db, f)
			if err := s.RegisterItem(ctx, testItem); err != nil {
				t.Fatal(err)
			}
			if err := s.SyncItem(ctx, testItem); err != nil {
				t.Fatal(err)
			}
			var old time.Time
			if err := db.QueryRow(ctx, `SELECT last_synced_at FROM pluggy_items WHERE item_id=$1`, testItem).Scan(&old); err != nil {
				t.Fatal(err)
			}
			f.mode.Store(mode)
			_ = s.SyncItem(ctx, testItem)
			var state string
			var current time.Time
			var complete bool
			if err := db.QueryRow(ctx, `SELECT status,last_synced_at,(coverage->>'complete_for_reported_products')::boolean FROM pluggy_items WHERE item_id=$1`, testItem).Scan(&state, &current, &complete); err != nil {
				t.Fatal(err)
			}
			if state != "partial" || !current.Equal(old) || complete {
				t.Fatalf("empty product accepted as complete: %s old=%s now=%s complete=%v", state, old, current, complete)
			}
			for _, table := range []string{"pluggy_accounts", "pluggy_transactions", "pluggy_investments"} {
				var count int
				if err := db.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE user_id=$1 AND active`, memory.UserID).Scan(&count); err != nil || count != 1 {
					t.Fatalf("%s lost last good active rows: %d %v", table, count, err)
				}
			}
		})
	}
}
