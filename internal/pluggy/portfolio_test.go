package pluggy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"openchris/conselho/internal/memory"
)

func portfolioSource(t *testing.T, db *pgxpool.Pool) string {
	t.Helper()
	id := memory.ID()
	_, err := db.Exec(context.Background(), `INSERT INTO pluggy_items(item_id,user_id,status,explicit_owner,consent_expires_at,last_synced_at,coverage) VALUES($1,$2,'connected',true,'2099-01-01',clock_timestamp(),'{"source_products":["INVESTMENTS"],"complete_for_reported_products":true}')`, id, memory.UserID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func portfolioPosition(t *testing.T, db *pgxpool.Pool, item, id, status, currency string, balance *string, active bool) {
	t.Helper()
	payload, _ := json.Marshal(Object{"status": status, "issuer": "Emissor de teste", "owner": "private-owner", "number": "private-account", "issuerCNPJ": "12.345.678/0001-00", "dueDate": "2027-12-10", "access_token": "private-token"})
	_, err := db.Exec(context.Background(), `INSERT INTO pluggy_investments(user_id,item_id,external_id,name,investment_type,balance,currency,observed_at,source_updated_at,content_hash,active,payload) VALUES($1,$2,$3,'Título de teste','FIXED_INCOME',$4::text::numeric,NULLIF($5,''),clock_timestamp(),clock_timestamp(),'fixture',$6,$7)`, memory.UserID, item, id, balance, currency, active, payload)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPortfolioSeparatesHoldingsWithdrawalsCurrenciesAndMissingValues(t *testing.T) {
	db := testDB(t)
	item := portfolioSource(t, db)
	for _, row := range []struct {
		id, status, currency, balance string
		active                        bool
	}{
		{"a", "ACTIVE", "BRL", "0.1", true}, {"b", "ACTIVE", "BRL", "0.2", true},
		{"usd", "ACTIVE", "USD", "2.125", true}, {"missing", "ACTIVE", "BRL", "", true},
		{"missing-currency", "ACTIVE", "", "10", true}, {"withdrawn", "TOTAL_WITHDRAWAL", "BRL", "900", true},
		{"unknown", "UNKNOWN", "BRL", "800", true}, {"absent", "ACTIVE", "BRL", "700", false},
	} {
		var value *string
		if row.balance != "" {
			v := row.balance
			value = &v
		}
		portfolioPosition(t, db, item, row.id, row.status, row.currency, value, row.active)
	}
	p, err := ReadPortfolio(context.Background(), db, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if p.ActiveCount != 5 || p.WithdrawnCount != 1 || p.UnknownCount != 1 || p.MissingValueCount != 2 || len(p.Positions) != 7 {
		t.Fatalf("wrong coverage: %+v", p)
	}
	for _, total := range p.Totals {
		switch total.Currency {
		case "BRL":
			if total.Balance == nil || *total.Balance != "0.3" || total.Count != 3 || total.ValuedCount != 2 {
				t.Fatalf("inexact BRL or missing treated as zero: %+v", total)
			}
		case "USD":
			if total.Balance == nil || *total.Balance != "2.125" {
				t.Fatal("mixed currencies", total)
			}
		case "N/D":
			if total.Balance != nil {
				t.Fatal("invented currency", total)
			}
		default:
			t.Fatal(total)
		}
	}
	if len(p.Totals) != 3 || len(p.Groups) != 3 || p.Revision == "" || p.SourceID == "" {
		t.Fatalf("missing aggregates: %+v", p)
	}
	raw, _ := json.Marshal(p)
	for _, secret := range []string{"private-owner", "private-account", "private-token", "12.345.678/0001-00", "payload", "external_id"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("leaked field", secret)
		}
	}
}

func TestPortfolioHonorsConsentAndSnapshotCompleteness(t *testing.T) {
	for _, change := range []string{"revoked", "expired", "superseded", "owner", "stale", "partial"} {
		t.Run(change, func(t *testing.T) {
			db := testDB(t)
			item := portfolioSource(t, db)
			v := "10.5"
			portfolioPosition(t, db, item, "a", "ACTIVE", "BRL", &v, true)
			before, err := ReadPortfolio(context.Background(), db, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			set := map[string]string{"revoked": "status='revoked'", "expired": "consent_expires_at='2020-01-01'", "superseded": "superseded_by=item_id", "owner": "explicit_owner=false", "stale": "last_synced_at=clock_timestamp()-interval '40 hours'", "partial": "status='partial',coverage=coverage||'{\"complete_for_reported_products\":false}'"}[change]
			if _, err = db.Exec(context.Background(), `UPDATE pluggy_items SET `+set+` WHERE item_id=$1`, item); err != nil {
				t.Fatal(err)
			}
			after, err := ReadPortfolio(context.Background(), db, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			if before.Revision == after.Revision {
				t.Fatal("source/consent change did not change revision")
			}
			if change == "stale" || change == "partial" {
				if after.ActiveCount != 1 || after.State == "available" {
					t.Fatal("discarded preserved snapshot or hid warning", after)
				}
			} else if after.ActiveCount != 0 || len(after.Positions) != 0 || len(after.Totals) != 0 || after.State == "available" {
				t.Fatal("used unauthorized source", after)
			}
		})
	}
}

func TestPortfolioBoundsDetailsButKeepsCompleteAggregates(t *testing.T) {
	db := testDB(t)
	item := portfolioSource(t, db)
	_, err := db.Exec(context.Background(), `INSERT INTO pluggy_investments(user_id,item_id,external_id,name,investment_type,balance,currency,observed_at,content_hash,payload) SELECT $1,$2,'asset-'||n,'Título '||n,'FIXED_INCOME',1,'BRL',clock_timestamp(),'hash','{"status":"ACTIVE"}' FROM generate_series(1,250) n`, memory.UserID, item)
	if err != nil {
		t.Fatal(err)
	}
	p, err := ReadPortfolio(context.Background(), db, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if p.ActiveCount != 250 || len(p.Positions) != 100 || p.Omitted != 150 || len(p.Totals) != 1 || p.Totals[0].Balance == nil || *p.Totals[0].Balance != "250" {
		t.Fatal("details limit truncated totals", p)
	}
	previous := p.Revision
	if _, err = db.Exec(context.Background(), `UPDATE pluggy_investments SET balance=2,version=version+1 WHERE external_id='asset-250'`); err != nil {
		t.Fatal(err)
	}
	p, err = ReadPortfolio(context.Background(), db, time.Now().UTC())
	if err != nil || p.Revision == previous {
		t.Fatal("omitted holding not included in revision", err)
	}
}

func TestPortfolioRevisionIgnoresIdenticalIngestion(t *testing.T) {
	db := testDB(t)
	item := portfolioSource(t, db)
	value := "10.00"
	portfolioPosition(t, db, item, "same-position", "ACTIVE", "BRL", &value, true)
	at := time.Now().UTC()
	before, err := ReadPortfolio(context.Background(), db, at)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(context.Background(), `UPDATE pluggy_items SET status='syncing',last_synced_at=last_synced_at+interval '1 minute',version=version+1; UPDATE pluggy_investments SET version=version+1,content_hash='new-ingestion',balance=10.0,observed_at=observed_at+interval '1 minute',source_updated_at=source_updated_at+interval '1 minute'`)
	if err != nil {
		t.Fatal(err)
	}
	after, err := ReadPortfolio(context.Background(), db, at.Add(time.Minute))
	if err != nil || before.Revision != after.Revision {
		t.Fatalf("identical ingestion changed revision: %s %s %v", before.Revision, after.Revision, err)
	}
	if _, err = db.Exec(context.Background(), `UPDATE pluggy_items SET last_source_sync=$1`, at.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	after, err = ReadPortfolio(context.Background(), db, at)
	if err != nil || !after.Stale || after.Revision == before.Revision {
		t.Fatalf("cached provider data became fresh: %+v %v", after, err)
	}
}

func TestStatusSeparatesWorkingConnectionsAndRecoveredFailures(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	working := portfolioSource(t, db)
	revoked := portfolioSource(t, db)
	superseded := portfolioSource(t, db)
	_, err := db.Exec(ctx, `UPDATE pluggy_items SET status='revoked',superseded_by=CASE WHEN item_id=$2::uuid THEN $3::uuid END WHERE item_id IN ($1,$2)`, revoked, superseded, working)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO pluggy_jobs(user_id,item_id,status,locked_at,finished_at) VALUES($1,$2,'failed',clock_timestamp()-interval '2 hours',clock_timestamp()-interval '1 hour'),($1,$3,'failed',clock_timestamp(),clock_timestamp()),($1,$4,'failed',clock_timestamp(),clock_timestamp())`, memory.UserID, working, revoked, superseded)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{}
	status, err := s.readStatus(ctx, db, Object{})
	if err != nil {
		t.Fatal(err)
	}
	if status["status"] != "connected_with_attention" || status["usable_connections"] != 1 || status["attention_connections"] != 1 || status["previous_connections"] != 1 {
		t.Fatalf("mixed health: %+v", status)
	}
	jobs := status["jobs"].(Object)
	if jobs["failed"] != int64(1) || jobs["historical_failed"] != int64(2) {
		t.Fatalf("old failures became current: %+v", jobs)
	}
}

func TestConnectionStatusUsesProviderFreshnessAndLocalFallback(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	item := portfolioSource(t, db)
	fresh, old := time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(-72*time.Hour)
	for _, tc := range []struct {
		name, stored, want string
		local, source      *time.Time
	}{
		{"cached bank data", "connected", "stale", &fresh, &old},
		{"old local data", "connected", "stale", &old, &fresh},
		{"fresh provider data", "connected", "connected", &fresh, &fresh},
		{"missing provider date uses fresh local", "connected", "connected", &fresh, nil},
		{"missing provider date uses old local", "connected", "stale", &old, nil},
		{"running sync does not renew data", "syncing", "stale", &fresh, &old},
		{"no local import", "connected", "stale", nil, &fresh},
		{"revocation takes precedence", "revoked", "revoked", &fresh, &old},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Exec(ctx, `UPDATE pluggy_items SET status=$1,last_synced_at=$2,last_source_sync=$3 WHERE item_id=$4`, tc.stored, tc.local, tc.source, item); err != nil {
				t.Fatal(err)
			}
			status, err := (&Service{}).readStatus(ctx, db, Object{})
			if err != nil {
				t.Fatal(err)
			}
			items := status["items"].([]Object)
			if len(items) != 1 || items[0]["status"] != tc.want || status["status"] != tc.want {
				t.Fatalf("incorrect connection freshness: %+v", status)
			}
			wantAttention := 1
			if tc.want == "connected" {
				wantAttention = 0
			}
			if status["attention_connections"] != wantAttention {
				t.Fatalf("connection attention disagrees with freshness: %+v", status)
			}
		})
	}
}
