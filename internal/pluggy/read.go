package pluggy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"openchris/conselho/internal/memory"
)

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *Service) baseStatus() Object {
	missing := []string{}
	for _, pair := range [][2]string{{"PLUGGY_CLIENT_ID", s.cfg.ClientID}, {"PLUGGY_CLIENT_SECRET", s.cfg.ClientSecret}, {"PLUGGY_WEBHOOK_SECRET", s.cfg.WebhookSecret}, {"PUBLIC_BASE_URL", s.cfg.PublicURL}} {
		if pair[1] == "" {
			missing = append(missing, pair[0])
		}
	}
	state := "disabled"
	if s.cfg.Enabled {
		state = "awaiting_authorization"
	}
	return Object{"enabled": s.cfg.Enabled, "configured": len(missing) == 0, "status": state, "missing_credentials": missing, "webhook_url": s.cfg.PublicURL, "items": []Object{}, "counts": Object{"items": 0, "accounts": 0, "transactions": 0, "investments": 0}, "last_synced_at": nil, "max_connections": 5, "source_update_hours": 24, "read_only": true, "reconciliation_required": true, "coverage": "unknown"}
}

// Status and Data only read our store. A dashboard GET cannot call a provider.
func (s *Service) Status(ctx context.Context) (map[string]any, error) {
	status := s.baseStatus()
	if !s.cfg.Enabled {
		return status, nil
	}
	if err := s.storage(); err != nil {
		return nil, err
	}
	return s.readStatus(ctx, s.db, status)
}
func (s *Service) readStatus(ctx context.Context, db queryer, status Object) (Object, error) {
	items, _, err := readObjects(ctx, db, `SELECT jsonb_build_object('id',item_id,'item_id',item_id,'connector_id',connector_id,'connector_name',connector_name,'name',connector_name,'connector',jsonb_build_object('id',connector_id,'name',connector_name),'status',CASE WHEN status<>'revoked' AND consent_expires_at<=clock_timestamp() THEN 'expired' WHEN status IN ('connected','syncing') AND (last_synced_at IS NULL OR least(last_synced_at,last_source_sync)<clock_timestamp()-interval '36 hours') THEN 'stale' ELSE status END,'provider_status',provider_status,'consent_expires_at',consent_expires_at,'last_source_sync',last_source_sync,'last_synced_at',last_synced_at,'last_attempt_at',last_attempt_at,'next_sync_at',next_sync_at,'last_error_code',last_error_code,'coverage',coverage,'superseded_by',superseded_by,'observed_at',observed_at,'version',version) FROM pluggy_items WHERE user_id=$1 ORDER BY registered_at DESC LIMIT 501`)
	if err != nil {
		return nil, err
	}
	status["items"] = items
	counts := Object{}
	for _, entry := range [][2]string{{"items", "pluggy_items"}, {"accounts", "pluggy_accounts"}, {"transactions", "pluggy_transactions"}, {"investments", "pluggy_investments"}} {
		where := ""
		if entry[0] != "items" {
			where = " AND active"
		}
		var count int64
		if err = db.QueryRow(ctx, `SELECT count(*) FROM `+entry[1]+` WHERE user_id=$1`+where, memory.UserID).Scan(&count); err != nil {
			return nil, err
		}
		counts[entry[0]] = count
	}
	status["counts"] = counts
	var last *time.Time
	if err = db.QueryRow(ctx, `SELECT max(last_synced_at) FROM pluggy_items WHERE user_id=$1 AND superseded_by IS NULL AND status NOT IN ('revoked','expired') AND (consent_expires_at IS NULL OR consent_expires_at>clock_timestamp())`, memory.UserID).Scan(&last); err != nil {
		return nil, err
	}
	status["last_synced_at"] = last
	var pending, failed, allFailed int64
	if err = db.QueryRow(ctx, `SELECT count(*) FILTER(WHERE j.status IN ('pending','running') AND i.superseded_by IS NULL),
	 count(*) FILTER(WHERE j.status='failed' AND i.superseded_by IS NULL AND (i.last_synced_at IS NULL OR coalesce(j.locked_at,j.created_at)>i.last_synced_at)),
	 count(*) FILTER(WHERE j.status='failed') FROM pluggy_jobs j LEFT JOIN pluggy_items i ON i.user_id=j.user_id AND i.item_id=j.item_id WHERE j.user_id=$1`, memory.UserID).Scan(&pending, &failed, &allFailed); err != nil {
		return nil, err
	}
	status["jobs"] = Object{"pending": pending, "failed": failed, "historical_failed": allFailed - failed}
	usable, attention, previous := 0, 0, 0
	if len(items) > 0 {
		state := "connected"
		rank := map[string]int{"connected": 0, "syncing": 1, "stale": 2, "partial": 3, "revoked": 4, "user_action_required": 5, "expired": 6, "failed": 7}
		for _, item := range items {
			if item["superseded_by"] != nil {
				previous++
				continue
			}
			current := textField(item, "status")
			if current == "connected" || current == "syncing" {
				usable++
			} else {
				attention++
			}
			if rank[current] > rank[state] {
				state = current
			}
		}
		if usable > 0 && attention > 0 {
			state = "connected_with_attention"
		}
		if usable+attention == 0 {
			state = "awaiting_authorization"
		}
		status["status"] = state
		status["coverage"] = "reported_products_only"
	}
	status["usable_connections"], status["attention_connections"], status["previous_connections"] = usable, attention, previous
	return status, nil
}
func (s *Service) Data(ctx context.Context) (map[string]any, error) {
	status := s.baseStatus()
	data := Object{"items": []Object{}, "accounts": []Object{}, "transactions": []Object{}, "investments": []Object{}, "source_provider": "pluggy", "read_only": true, "reconciliation_required": true, "limit_per_product": 500, "truncated": Object{}}
	if !s.cfg.Enabled {
		data["status"] = status
		return data, nil
	}
	if err := s.storage(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	status, err = s.readStatus(ctx, tx, status)
	if err != nil {
		return nil, err
	}
	data["items"] = status["items"]
	data["status"] = status
	queries := [][2]string{
		{"accounts", `SELECT jsonb_build_object('id',external_id,'external_id',external_id,'item_id',item_id,'name',name,'type',account_type,'subtype',subtype,'balance',balance::text,'currency',currency,'source_updated_at',source_updated_at,'observed_at',observed_at,'ingested_at',ingested_at,'version',version,'active',active,'payload',payload) FROM pluggy_accounts WHERE user_id=$1 ORDER BY active DESC,observed_at DESC,external_id LIMIT 501`},
		{"transactions", `SELECT jsonb_build_object('id',external_id,'external_id',external_id,'item_id',item_id,'account_id',account_external_id,'description',description,'amount',amount::text,'currency',currency,'date',effective_at,'status',provider_status,'source_updated_at',source_updated_at,'observed_at',observed_at,'ingested_at',ingested_at,'version',version,'active',active,'payload',payload) FROM pluggy_transactions WHERE user_id=$1 ORDER BY active DESC,effective_at DESC NULLS LAST,external_id LIMIT 501`},
		{"investments", `SELECT jsonb_build_object('id',external_id,'external_id',external_id,'item_id',item_id,'name',name,'type',investment_type,'balance',balance::text,'amount',balance::text,'quantity',quantity::text,'unit_price',unit_price::text,'price_currency',price_currency,'currency',currency,'date',effective_at,'source_updated_at',source_updated_at,'observed_at',observed_at,'ingested_at',ingested_at,'version',version,'active',active,'payload',payload) FROM pluggy_investments WHERE user_id=$1 ORDER BY active DESC,observed_at DESC,external_id LIMIT 501`},
	}
	for _, query := range queries {
		rows, truncated, e := readObjects(ctx, tx, query[1])
		if e != nil {
			return nil, e
		}
		data[query[0]] = rows
		data["truncated"].(Object)[query[0]] = truncated
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return data, nil
}
func readObjects(ctx context.Context, db queryer, sql string) ([]Object, bool, error) {
	rows, err := db.Query(ctx, sql, memory.UserID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []Object{}
	truncated := false
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, false, err
		}
		if len(out) == 500 {
			truncated = true
			continue
		}
		var row Object
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err = decoder.Decode(&row); err != nil {
			return nil, false, errors.New("invalid stored Pluggy data")
		}
		out = append(out, row)
	}
	return out, truncated, rows.Err()
}
