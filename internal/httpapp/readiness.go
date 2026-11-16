package httpapp

import (
	"context"
	"net/http"
	"time"
)

func (a *App) runtimeMode() string {
	if a.config.Production {
		return "production"
	}
	return "manual_finance_simulated_integrations"
}

func (a *App) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.store.DB.Ping(ctx); err != nil {
		respond(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "mode": a.runtimeMode(), "error": "Banco indisponível."})
		return
	}
	respond(w, http.StatusOK, map[string]string{"status": "ok", "mode": a.runtimeMode()})
}

func (a *App) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	var migrated bool
	err := a.store.DB.QueryRow(ctx, `SELECT count(*)=10 FROM schema_migrations WHERE version BETWEEN 1 AND 10`).Scan(&migrated)
	if err == nil && migrated {
		// Resolve required columns even when the tables are empty.
		_, err = a.store.DB.Exec(ctx, `
			SELECT event_id,status,lease_token,lease_expires_at FROM council_jobs LIMIT 0;
			SELECT source_event_id,result,envelope FROM council_results LIMIT 0;
			SELECT token_hash,csrf,expires_at FROM sessions LIMIT 0;
			SELECT id,user_id,name,color,version FROM finance_categories LIMIT 0;
			SELECT id,user_id,transaction_date,description,category_id,account,status,amount_cents,recurring FROM finance_transactions LIMIT 0;
			SELECT id,user_id,observation_date,assets_cents,liabilities_cents,net_cents FROM finance_net_worth LIMIT 0;
			SELECT id,user_id,entity_type,entity_id,version,action,payload FROM finance_audit LIMIT 0;
			SELECT id,user_id,channel,direction,body FROM conversation_events LIMIT 0;
			SELECT event_id,status FROM jobs LIMIT 0;
			SELECT id,user_id,predicate,value,version FROM memory_facts LIMIT 0;
			SELECT id,user_id,source_event_id,channel,body,outcome FROM episodes LIMIT 0;
			SELECT sequence,user_id,event_type,payload FROM stream_events LIMIT 0;
			SELECT bot_id,update_id,incoming_event_id,status FROM telegram_inbox LIMIT 0;
			SELECT bot_id,update_id,status,attempts FROM telegram_outbox LIMIT 0;
			SELECT item_id,user_id,status,last_synced_at,consent_expires_at,wealth_loans FROM pluggy_items LIMIT 0;
			SELECT user_id,external_id,item_id,payload FROM pluggy_accounts LIMIT 0;
			SELECT user_id,external_id,item_id,payload FROM pluggy_transactions LIMIT 0;
			SELECT user_id,external_id,item_id,payload FROM pluggy_investments LIMIT 0;
			SELECT user_id,version,data,confirmed_at FROM finance_profile LIMIT 0;
			SELECT user_id,transaction_id,category,kind,version FROM finance_classifications LIMIT 0;
			SELECT user_id,match_key,category,kind,version FROM finance_classification_rules LIMIT 0;
			SELECT user_id,entity,entity_id,version,payload FROM finance_personal_audit LIMIT 0;
			SELECT reserved_calls,quota_day FROM autonomy_runs LIMIT 0;
			SELECT budget_wait_until FROM autonomy_state LIMIT 0;
			SELECT body,financial_revision,cancellation_code FROM autonomy_notices LIMIT 0`)
	}
	if err != nil || !migrated {
		respond(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "mode": a.runtimeMode(), "error": "Banco ou estrutura indisponível."})
		return
	}
	respond(w, http.StatusOK, map[string]string{"status": "ready", "mode": a.runtimeMode()})
}
