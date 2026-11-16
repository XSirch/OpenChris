package pluggy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"openchris/conselho/internal/memory"
)

// One source read is deliberately delayed while the source advances. Without
// a cross-instance lease, its old response can overwrite the newer observation.
func concurrentServices(t *testing.T, db *pgxpool.Pool) (*Service, *Service, <-chan struct{}, func(), *atomic.Int32) {
	t.Helper()
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	var first atomic.Bool
	first.Store(true)
	var calls atomic.Int32
	fixture := &fixture{owner: memory.UserID, itemID: testItem}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/accounts" {
			fixture.handler(w, r)
			return
		}
		balance := "20"
		if first.CompareAndSwap(true, false) {
			close(started)
			select {
			case <-release:
				balance = "10"
			case <-r.Context().Done():
				return
			}
		}
		fmt.Fprint(w, `{"page":1,"totalPages":1,"results":[{"id":"`+testAccount+`","itemId":"`+testItem+`","balance":`+balance+`,"currencyCode":"USD","updatedAt":null}]}`)
	}))
	t.Cleanup(server.Close)
	unlock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unlock)
	cfg := Config{Enabled: true, ClientID: "id", ClientSecret: "server-secret", WebhookSecret: testWebhookSecret, PublicURL: "https://conselho.example/webhooks/pluggy", APIBaseURL: server.URL, HTTPClient: server.Client()}
	a, err := New(db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.RegisterItem(context.Background(), testItem); err != nil {
		t.Fatal(err)
	}
	return a, b, started, unlock, &calls
}

func TestConcurrentServicesLeaseOneItemBeforeAnyProviderRead(t *testing.T) {
	db := testDB(t)
	a, b, started, release, calls := concurrentServices(t, db)
	ctx := context.Background()
	firstResult := make(chan error, 1)
	go func() { firstResult <- a.SyncItem(ctx, testItem) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("first source read did not start")
	}
	before := calls.Load()
	secondCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	secondErr := b.SyncItem(secondCtx, testItem)
	release()
	firstErr := <-firstResult
	if secondErr == nil || !strings.Contains(secondErr.Error(), "busy") {
		t.Fatalf("concurrent sync was not deferred before network: %v", secondErr)
	}
	if firstErr != nil {
		t.Fatal(firstErr)
	}
	// Once the first lease is released, a fresh second read can become current.
	if after := calls.Load(); after != before+2 {
		t.Fatalf("busy instance queried provider: before=%d after=%d", before, after)
	}
	if err := b.SyncItem(ctx, testItem); err != nil {
		t.Fatalf("lease was not released: %v", err)
	}
	var balance string
	if err := db.QueryRow(ctx, `SELECT balance::text FROM pluggy_accounts WHERE external_id=$1`, testAccount).Scan(&balance); err != nil || balance != "20" {
		t.Fatalf("fresh observation was overwritten: %s %v", balance, err)
	}
}

func TestBusyWorkerJobRetriesWithoutConsumingFailureAttempts(t *testing.T) {
	db := testDB(t)
	a, b, started, release, calls := concurrentServices(t, db)
	ctx := context.Background()
	firstResult := make(chan error, 1)
	go func() { _, err := a.processOne(ctx); firstResult <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("first worker did not start")
	}
	// An authenticated second event can arrive while another replica owns this item.
	event := []byte(`{"event":"item/updated","eventId":"d876fd7c-e9bd-4c4c-bd46-cc96c62aac25","itemId":"` + testItem + `","clientUserId":"` + memory.UserID + `"}`)
	if _, err := b.HandleWebhook(ctx, testWebhookSecret, event); err != nil {
		t.Fatal(err)
	}
	if err := b.QueueSync(ctx, testItem); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE pluggy_jobs SET attempts=5 WHERE event_id IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	did, secondErr := b.processOne(ctx)
	var state, code string
	var attempts int
	queryErr := db.QueryRow(ctx, `SELECT status,attempts,last_error_code FROM pluggy_jobs WHERE event_id IS NOT NULL`).Scan(&state, &attempts, &code)
	release()
	firstErr := <-firstResult
	if !did || secondErr == nil || !strings.Contains(secondErr.Error(), "busy") || queryErr != nil || state != "pending" || code != "SYNC_BUSY" || attempts != 5 {
		t.Fatalf("busy job dead-lettered: did=%v err=%v state=%s attempts=%d code=%s query=%v", did, secondErr, state, attempts, code, queryErr)
	}
	if firstErr != nil {
		t.Fatal(firstErr)
	}
	if calls.Load() != before+2 {
		t.Fatal("second worker made provider calls while busy")
	}
	var jobs int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM pluggy_jobs`).Scan(&jobs); err != nil || jobs != 2 {
		t.Fatalf("QueueSync duplicated the pending periodic job: %d %v", jobs, err)
	}
	if _, err := db.Exec(ctx, `UPDATE pluggy_jobs SET available_at=clock_timestamp()-interval '1 second' WHERE status='pending'`); err != nil {
		t.Fatal(err)
	}
	if did, err := b.processOne(ctx); !did || err != nil {
		t.Fatalf("deferred job did not resume: %v %v", did, err)
	}
}

func TestCanceledSyncReleasesLeaseForAnotherInstance(t *testing.T) {
	db := testDB(t)
	a, b, started, release, _ := concurrentServices(t, db)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- a.SyncItem(ctx, testItem) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("first source read did not start")
	}
	cancel()
	release()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled sync: %v", err)
	}
	if err := b.SyncItem(context.Background(), testItem); err != nil {
		t.Fatalf("cancellation leaked item lease: %v", err)
	}
}

func TestSyncLeaseWorksWithSingleConnectionPool(t *testing.T) {
	db := testDB(t)
	cfg := db.Config()
	cfg.MaxConns = 1
	cfg.MinConns = 0
	single, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(single.Close)
	s := testService(t, single, &fixture{owner: memory.UserID, itemID: testItem})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err = s.RegisterItem(ctx, testItem); err != nil {
		t.Fatal(err)
	}
	if err = s.SyncItem(ctx, testItem); err != nil {
		t.Fatalf("lease exhausted a one-connection pool: %v", err)
	}
}

// A renewed consent has a different item lease but the same external product
// identities. Its committed ownership must survive an older in-flight read.
func TestRenewedItemCommitCannotBeReversedByOlderInFlightSync(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	newItem := "00000000-0000-4000-8000-000000000077"
	started := make(chan struct{})
	resume := make(chan struct{})
	var resumeOnce sync.Once
	release := func() { resumeOnce.Do(func() { close(resume) }) }
	var oldReads atomic.Int32
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/auth":
			fmt.Fprint(w, `{"apiKey":"key"}`)
		case "/items/" + testItem, "/items/" + newItem:
			itemID := strings.TrimPrefix(r.URL.Path, "/items/")
			fmt.Fprintf(w, `{"id":%q,"clientUserId":%q,"status":"UPDATED","executionStatus":"SUCCESS","consentExpiresAt":"2099-01-01T00:00:00Z","lastUpdatedAt":"2026-10-28T12:00:00Z","connector":{"id":200,"name":"MeuPluggy"},"products":["ACCOUNTS","TRANSACTIONS","INVESTMENTS"]}`, itemID, memory.UserID)
		case "/accounts":
			itemID := r.URL.Query().Get("itemId")
			balance, name := "20", "Renewed account"
			if itemID == testItem {
				balance, name = "10", "Obsolete account"
				if oldReads.Add(1) == 2 {
					close(started)
					select {
					case <-resume:
					case <-r.Context().Done():
						return
					}
				}
			}
			fmt.Fprintf(w, `{"page":1,"totalPages":1,"results":[{"id":%q,"itemId":%q,"name":%q,"balance":%s,"currencyCode":"USD"}]}`, testAccount, itemID, name, balance)
		case "/v2/transactions":
			fmt.Fprintf(w, `{"next":null,"results":[{"id":"same-transaction","accountId":%q,"amount":5,"currencyCode":"USD"}]}`, testAccount)
		case "/investments":
			balance := "20"
			if r.URL.Query().Get("itemId") == testItem {
				balance = "10"
			}
			fmt.Fprintf(w, `{"page":1,"totalPages":1,"results":[{"id":"same-investment","balance":%s,"currencyCode":"USD"}]}`, balance)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(release)
	cfg := Config{Enabled: true, ClientID: "id", ClientSecret: "server-secret", WebhookSecret: testWebhookSecret, PublicURL: "https://conselho.example/webhooks/pluggy", APIBaseURL: server.URL, HTTPClient: server.Client()}
	a, err := New(db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.RegisterItem(ctx, testItem); err != nil {
		t.Fatal(err)
	}
	if err = a.SyncItem(ctx, testItem); err != nil {
		t.Fatal(err)
	}
	oldResult := make(chan error, 1)
	go func() { _, err := a.processOne(ctx); oldResult <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("old connection did not start its delayed source read")
	}
	if err = b.RegisterItem(ctx, newItem); err != nil {
		t.Fatal(err)
	}
	if err = b.SyncItem(ctx, newItem); err != nil {
		t.Fatal(err)
	}
	release()
	if err = <-oldResult; err != nil {
		t.Fatalf("obsolete job did not finish as replaced consent: %v", err)
	}
	var oldState, newState string
	var oldSuccessor, newSuccessor *string
	if err = db.QueryRow(ctx, `SELECT status,superseded_by::text FROM pluggy_items WHERE item_id=$1`, testItem).Scan(&oldState, &oldSuccessor); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT status,superseded_by::text FROM pluggy_items WHERE item_id=$1`, newItem).Scan(&newState, &newSuccessor); err != nil {
		t.Fatal(err)
	}
	if oldState != "revoked" || oldSuccessor == nil || *oldSuccessor != newItem || newState != "connected" || newSuccessor != nil {
		t.Fatalf("renewed consent was reversed or cycled: old=%s->%v new=%s->%v", oldState, oldSuccessor, newState, newSuccessor)
	}
	for _, table := range []string{"pluggy_accounts", "pluggy_transactions", "pluggy_investments"} {
		var owner string
		var count int
		if err = db.QueryRow(ctx, `SELECT min(item_id::text),count(*) FROM `+table+` WHERE active`).Scan(&owner, &count); err != nil || owner != newItem || count != 1 {
			t.Fatalf("%s moved to obsolete consent: owner=%s rows=%d err=%v", table, owner, count, err)
		}
	}
	var balance, name string
	if err = db.QueryRow(ctx, `SELECT balance::text,name FROM pluggy_accounts WHERE external_id=$1`, testAccount).Scan(&balance, &name); err != nil || balance != "20" || name != "Renewed account" {
		t.Fatalf("stale payload replaced current source: %s %s %v", balance, name, err)
	}
	var staleVersions int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM pluggy_snapshots WHERE item_id=$1 AND version>1`, testItem).Scan(&staleVersions); err != nil || staleVersions != 0 {
		t.Fatalf("stale snapshots were committed after replacement: %d %v", staleVersions, err)
	}
	var jobState string
	var jobCode *string
	if err = db.QueryRow(ctx, `SELECT status,last_error_code FROM pluggy_jobs WHERE item_id=$1`, testItem).Scan(&jobState, &jobCode); err != nil || jobState != "complete" || jobCode == nil || *jobCode != "CONSENT_REPLACED" {
		t.Fatalf("obsolete job was not terminal: %s %v %v", jobState, jobCode, err)
	}
	before := calls.Load()
	if err = a.SyncItem(ctx, testItem); err == nil || calls.Load() != before {
		t.Fatalf("replaced consent made another source read: %v", err)
	}
}
