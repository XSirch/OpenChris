package pluggy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"openchris/conselho/internal/memory"
)

var decimalPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

var errEmptySourceProduct = errors.New("empty Pluggy product response; previous observations preserved")
var errItemSuperseded = errors.New("Pluggy item consent was superseded")

// Numeric parameters travel as text and PostgreSQL casts them directly to
// numeric. No financial amount passes through a floating-point conversion.
func decimal(row Object, key string) (any, error) {
	value, exists := row[key]
	if !exists || value == nil {
		return nil, nil
	}
	var text string
	switch n := value.(type) {
	case json.Number:
		text = n.String()
	case string:
		text = n
	default:
		return nil, errors.New("invalid Pluggy numeric value")
	}
	if len(text) > 160 || !decimalPattern.MatchString(text) {
		return nil, errors.New("invalid Pluggy numeric value")
	}
	if index := strings.IndexAny(text, "eE"); index >= 0 {
		exp, err := strconv.Atoi(text[index+1:])
		if err != nil || exp > 1000 || exp < -1000 {
			return nil, errors.New("Pluggy numeric exponent exceeds limit")
		}
	}
	return text, nil
}

func (s *Service) SyncItem(ctx context.Context, itemID string) error {
	if err := s.storage(); err != nil {
		return err
	}
	if !uuidPattern.MatchString(itemID) {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	ctx = withResponseBudget(ctx)
	conn, release, err := s.acquireSyncLease(ctx, itemID)
	if err != nil {
		return err
	}
	defer release()
	var explicit bool
	var superseded *string
	if err := conn.QueryRow(ctx, `SELECT explicit_owner,superseded_by::text FROM pluggy_items WHERE user_id=$1 AND item_id=$2`, memory.UserID, itemID).Scan(&explicit, &superseded); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalid
		}
		return err
	}
	if superseded != nil {
		return errItemSuperseded
	}
	if _, err := conn.Exec(ctx, `UPDATE pluggy_items SET last_attempt_at=clock_timestamp() WHERE user_id=$1 AND item_id=$2`, memory.UserID, itemID); err != nil {
		return err
	}
	item, err := s.client.Item(ctx, itemID)
	if err != nil {
		return s.fail(ctx, conn, itemID, nil, "", err)
	}
	owner := textField(item, "clientUserId")
	if owner != memory.UserID && !(explicit && owner == "") {
		return s.fail(ctx, conn, itemID, nil, "failed", ErrUnauthorized)
	}
	for _, key := range []string{"consentExpiresAt", "lastUpdatedAt"} {
		if item[key] != nil && timestamp(item, key) == nil {
			return s.fail(ctx, conn, itemID, nil, "partial", errors.New("invalid Pluggy item timestamp"))
		}
	}
	state, code := itemState(item, time.Now())
	if state != "connected" {
		return s.recordState(ctx, conn, itemID, item, state, code)
	}
	accounts, err := s.client.Accounts(ctx, itemID)
	if err != nil {
		return s.fail(ctx, conn, itemID, item, "partial", err)
	}
	if len(accounts) > 500 {
		return s.fail(ctx, conn, itemID, item, "partial", errors.New("Pluggy account limit exceeded"))
	}
	transactions := []Object{}
	if productExpected(item, "TRANSACTIONS") {
		for _, account := range accounts {
			accountID := textField(account, "id")
			if !uuidPattern.MatchString(accountID) {
				return s.fail(ctx, conn, itemID, item, "partial", ErrInvalid)
			}
			rows, e := s.client.Transactions(ctx, accountID)
			if e != nil {
				return s.fail(ctx, conn, itemID, item, "partial", e)
			}
			for _, row := range rows {
				if row["accountId"] != nil && row["accountId"] != accountID {
					return s.fail(ctx, conn, itemID, item, "partial", errors.New("Pluggy transaction account mismatch"))
				}
				row["accountId"] = accountID
			}
			transactions = append(transactions, rows...)
			if len(transactions) > maxRecords {
				return s.fail(ctx, conn, itemID, item, "partial", errors.New("Pluggy transaction limit exceeded"))
			}
		}
	}
	investments := []Object{}
	if productExpected(item, "INVESTMENTS") {
		investments, err = s.client.Investments(ctx, itemID)
		if err != nil {
			return s.fail(ctx, conn, itemID, item, "partial", err)
		}
	}
	if len(accounts)+len(transactions)+len(investments) > maxRecords {
		return s.fail(ctx, conn, itemID, item, "partial", errors.New("Pluggy snapshot record limit exceeded"))
	}
	for _, records := range [][]Object{accounts, transactions, investments} {
		for _, row := range records {
			if id := textField(row, "id"); id == "" || len(id) > 200 {
				return s.fail(ctx, conn, itemID, item, "partial", ErrInvalid)
			}
			if row["itemId"] != nil && row["itemId"] != itemID {
				return s.fail(ctx, conn, itemID, item, "partial", errors.New("Pluggy product item mismatch"))
			}
			if row["updatedAt"] != nil && timestamp(row, "updatedAt") == nil {
				return s.fail(ctx, conn, itemID, item, "partial", errors.New("invalid Pluggy product timestamp"))
			}
			for _, key := range []string{"balance", "amount", "quantity", "unitValue"} {
				if _, err = decimal(row, key); err != nil {
					return s.fail(ctx, conn, itemID, item, "partial", err)
				}
			}
		}
	}
	loans := s.readWealthLoans(ctx, itemID, item)
	if err = s.persist(ctx, conn, itemID, item, accounts, transactions, investments, loans); err != nil {
		return s.fail(ctx, conn, itemID, item, "partial", err)
	}
	return nil
}
func productExpected(item Object, product string) bool {
	// MeuPluggy proxy items can omit available investments from collected products.
	// Read that authorized product when the personal connector explicitly supports it.
	if product == "INVESTMENTS" {
		connector, _ := item["connector"].(map[string]any)
		if textField(connector, "name") == "MeuPluggy" {
			if supported, ok := connector["products"].([]any); ok {
				for _, value := range supported {
					if value == product {
						return true
					}
				}
			}
		}
	}
	products, ok := item["products"].([]any)
	if !ok {
		return true
	}
	for _, value := range products {
		if value == product {
			return true
		}
	}
	return false
}
func failureCode(err error) string {
	if errors.Is(err, ErrBusy) {
		return "SYNC_BUSY"
	}
	if errors.Is(err, errEmptySourceProduct) {
		return "EMPTY_SOURCE_PRODUCT"
	}
	var api *APIError
	if errors.As(err, &api) {
		return fmt.Sprintf("API_HTTP_%d", api.Status)
	}
	if errors.Is(err, ErrUnauthorized) {
		return "OWNERSHIP_MISMATCH"
	}
	if errors.Is(err, ErrCapacity) {
		return "PERSONAL_CONNECTION_LIMIT"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "SOURCE_TIMEOUT"
	}
	return "SOURCE_READ_FAILED"
}
func (s *Service) fail(ctx context.Context, conn *pgxpool.Conn, itemID string, item Object, state string, cause error) error {
	if errors.Is(cause, errItemSuperseded) {
		return cause
	}
	var api *APIError
	if errors.As(cause, &api) {
		switch api.Status {
		case 404, 410:
			if item == nil {
				state = "revoked"
			}
		case 429:
			state = "stale"
		}
	}
	if state == "" {
		state = "failed"
	}
	// A caller cancellation must not delete or replace financial observations.
	if ctx.Err() != nil {
		return cause
	}
	if err := s.recordState(ctx, conn, itemID, item, state, failureCode(cause)); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}
func (s *Service) recordState(ctx context.Context, conn *pgxpool.Conn, itemID string, item Object, state, code string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var meta []byte
	var expires, source *time.Time
	if item != nil {
		meta, _ = json.Marshal(safeItem(item))
		expires = timestamp(item, "consentExpiresAt")
		source = timestamp(item, "lastUpdatedAt")
	}
	_, err = tx.Exec(ctx, `UPDATE pluggy_items SET status=$3,last_error_code=$4,observed_at=clock_timestamp(),next_sync_at=clock_timestamp()+$5::bigint*interval '1 millisecond',coverage=coverage||jsonb_build_object('complete_for_reported_products',false,'last_attempt_status',$3::text,'last_attempt_error_code',$4::text,'last_attempt_at',clock_timestamp()),metadata=CASE WHEN $6::jsonb IS NULL THEN metadata ELSE $6::jsonb END,consent_expires_at=CASE WHEN $6::jsonb IS NULL THEN consent_expires_at ELSE $7 END,last_source_sync=CASE WHEN $6::jsonb IS NULL THEN last_source_sync ELSE $8 END,provider_status=CASE WHEN $6::jsonb IS NULL THEN provider_status ELSE $9 END WHERE user_id=$1 AND item_id=$2`, memory.UserID, itemID, state, nilString(code), s.cfg.SyncInterval.Milliseconds(), meta, expires, source, nullText(item, "status"))
	if err != nil {
		return err
	}
	b, _ := json.Marshal(Object{"status": state, "error_code": nilString(code)})
	if _, err = tx.Exec(ctx, `INSERT INTO pluggy_audit(user_id,item_id,action,payload) VALUES($1,$2,'state_observed',$3)`, memory.UserID, itemID, b); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) persist(ctx context.Context, conn *pgxpool.Conn, itemID string, item Object, accounts, transactions, investments []Object, loans []wealthLoan) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(2026102804)`); err != nil {
		return err
	}
	// Another item can replace this consent while its network reads are in
	// flight. Revalidate under the commit lock before touching any predecessor
	// or product, otherwise the old item could reverse the new ownership.
	var successor *string
	if err = tx.QueryRow(ctx, `SELECT superseded_by::text FROM pluggy_items WHERE user_id=$1 AND item_id=$2 FOR UPDATE`, memory.UserID, itemID).Scan(&successor); err != nil {
		return err
	}
	if successor != nil {
		return errItemSuperseded
	}
	if err = guardNonemptyProducts(ctx, tx, itemID, accounts, transactions, investments); err != nil {
		return err
	}
	// External product ids are stronger evidence of a renewed connection than the
	// connector name. Retire only predecessors sharing those verified identities.
	accountIDs, investmentIDs := []string{}, []string{}
	for _, row := range accounts {
		accountIDs = append(accountIDs, textField(row, "id"))
	}
	for _, row := range investments {
		investmentIDs = append(investmentIDs, textField(row, "id"))
	}
	_, err = tx.Exec(ctx, `UPDATE pluggy_items SET status='revoked',superseded_by=$2,last_error_code='CONSENT_REPLACED' WHERE user_id=$1 AND item_id<>$2 AND item_id IN (SELECT item_id FROM pluggy_accounts WHERE user_id=$1 AND external_id=ANY($3::text[]) UNION SELECT item_id FROM pluggy_investments WHERE user_id=$1 AND external_id=ANY($4::text[]))`, memory.UserID, itemID, accountIDs, investmentIDs)
	if err != nil {
		return err
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM pluggy_items WHERE user_id=$1 AND item_id<>$2 AND last_synced_at IS NOT NULL AND superseded_by IS NULL AND status NOT IN ('expired','revoked') AND (consent_expires_at IS NULL OR consent_expires_at>clock_timestamp())`, memory.UserID, itemID).Scan(&count); err != nil {
		return err
	}
	if count >= 5 {
		return ErrCapacity
	}
	now := time.Now().UTC()
	for _, table := range []string{"pluggy_accounts", "pluggy_transactions", "pluggy_investments"} {
		if _, err = tx.Exec(ctx, `UPDATE `+table+` SET active=false WHERE user_id=$1 AND item_id=$2`, memory.UserID, itemID); err != nil {
			return err
		}
	}
	for _, row := range accounts {
		if err = saveRecord(ctx, tx, itemID, "account", row, now); err != nil {
			return err
		}
	}
	for _, row := range transactions {
		if err = saveRecord(ctx, tx, itemID, "transaction", row, now); err != nil {
			return err
		}
	}
	for _, row := range investments {
		if err = saveRecord(ctx, tx, itemID, "investment", row, now); err != nil {
			return err
		}
	}
	meta := safeItem(item)
	b, _ := json.Marshal(meta)
	hash := contentHash(b)
	coverage, _ := json.Marshal(Object{"accounts": len(accounts), "transactions": len(transactions), "investments": len(investments), "accounts_observed_at": now, "transactions_observed_at": now, "investments_observed_at": now, "source_products": item["products"], "complete_for_reported_products": true, "reconciliation_required": true})
	var version int
	err = tx.QueryRow(ctx, `UPDATE pluggy_items SET status='connected',provider_status=$3,consent_expires_at=$4,last_source_sync=$5,last_synced_at=$6::timestamptz,last_attempt_at=$6::timestamptz,next_sync_at=$6::timestamptz+$7::bigint*interval '1 millisecond',last_error_code=NULL,coverage=$8,version=CASE WHEN metadata<>$9::jsonb THEN version+1 ELSE version END,metadata=$9,observed_at=$6::timestamptz WHERE user_id=$1 AND item_id=$2 RETURNING version`, memory.UserID, itemID, nullText(item, "status"), timestamp(item, "consentExpiresAt"), timestamp(item, "lastUpdatedAt"), now, s.cfg.SyncInterval.Milliseconds(), coverage, b).Scan(&version)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE pluggy_items SET wealth_loans=$3 WHERE user_id=$1 AND item_id=$2`, memory.UserID, itemID, loans); err != nil {
		return err
	}
	if err = saveSnapshot(ctx, tx, itemID, "item", itemID, version, hash, b, nullText(item, "lastUpdatedAt"), now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO pluggy_audit(user_id,item_id,action,payload) VALUES($1,$2,'snapshot_committed',$3)`, memory.UserID, itemID, coverage); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Empty lists are not evidence of liquidation. Guard each product and every
// account's transaction history before updating any current row or snapshot.
func guardNonemptyProducts(ctx context.Context, tx pgx.Tx, itemID string, accounts, transactions, investments []Object) error {
	var oldAccounts, oldTransactions, oldInvestments int
	if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM pluggy_accounts WHERE user_id=$1 AND item_id=$2 AND active),(SELECT count(*) FROM pluggy_transactions WHERE user_id=$1 AND item_id=$2 AND active),(SELECT count(*) FROM pluggy_investments WHERE user_id=$1 AND item_id=$2 AND active)`, memory.UserID, itemID).Scan(&oldAccounts, &oldTransactions, &oldInvestments); err != nil {
		return err
	}
	if (oldAccounts > 0 && len(accounts) == 0) || (oldTransactions > 0 && len(transactions) == 0) || (oldInvestments > 0 && len(investments) == 0) {
		return errEmptySourceProduct
	}
	newAccountIDs := map[string]bool{}
	transactionCounts := map[string]int{}
	for _, row := range accounts {
		newAccountIDs[textField(row, "id")] = true
	}
	for _, row := range transactions {
		transactionCounts[textField(row, "accountId")]++
	}
	rows, err := tx.Query(ctx, `SELECT external_id FROM pluggy_accounts WHERE user_id=$1 AND item_id=$2 AND active`, memory.UserID, itemID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !newAccountIDs[id] {
			rows.Close()
			return errEmptySourceProduct
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = tx.Query(ctx, `SELECT DISTINCT account_external_id FROM pluggy_transactions WHERE user_id=$1 AND item_id=$2 AND active AND account_external_id IS NOT NULL`, memory.UserID, itemID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return err
		}
		if transactionCounts[id] == 0 {
			return errEmptySourceProduct
		}
	}
	return rows.Err()
}

func saveRecord(ctx context.Context, tx pgx.Tx, itemID, kind string, row Object, now time.Time) error {
	payload := scrub(row)
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	hash := contentHash(b)
	id := textField(row, "id")
	var version int
	var effective any
	switch kind {
	case "account":
		balance, _ := decimal(row, "balance")
		err = tx.QueryRow(ctx, `INSERT INTO pluggy_accounts(user_id,external_id,item_id,name,account_type,subtype,balance,currency,source_updated_at,observed_at,content_hash,payload) VALUES($1,$2,$3,$4,$5,$6,$7::text::numeric,$8,$9,$10,$11,$12) ON CONFLICT(user_id,external_id) DO UPDATE SET item_id=EXCLUDED.item_id,name=EXCLUDED.name,account_type=EXCLUDED.account_type,subtype=EXCLUDED.subtype,balance=EXCLUDED.balance,currency=EXCLUDED.currency,source_updated_at=EXCLUDED.source_updated_at,observed_at=EXCLUDED.observed_at,active=true,version=CASE WHEN pluggy_accounts.content_hash<>EXCLUDED.content_hash OR pluggy_accounts.item_id<>EXCLUDED.item_id THEN pluggy_accounts.version+1 ELSE pluggy_accounts.version END,content_hash=EXCLUDED.content_hash,payload=EXCLUDED.payload RETURNING version`, memory.UserID, id, itemID, nullText(row, "name"), nullText(row, "type"), nullText(row, "subtype"), balance, nullText(row, "currencyCode"), timestamp(row, "updatedAt"), now, hash, b).Scan(&version)
		effective = nullText(row, "updatedAt")
	case "transaction":
		amount, _ := decimal(row, "amount")
		err = tx.QueryRow(ctx, `INSERT INTO pluggy_transactions(user_id,external_id,item_id,account_external_id,description,amount,currency,effective_at,provider_status,source_updated_at,observed_at,content_hash,payload) VALUES($1,$2,$3,$4,$5,$6::text::numeric,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(user_id,external_id) DO UPDATE SET item_id=EXCLUDED.item_id,account_external_id=EXCLUDED.account_external_id,description=EXCLUDED.description,amount=EXCLUDED.amount,currency=EXCLUDED.currency,effective_at=EXCLUDED.effective_at,provider_status=EXCLUDED.provider_status,source_updated_at=EXCLUDED.source_updated_at,observed_at=EXCLUDED.observed_at,active=true,version=CASE WHEN pluggy_transactions.content_hash<>EXCLUDED.content_hash OR pluggy_transactions.item_id<>EXCLUDED.item_id THEN pluggy_transactions.version+1 ELSE pluggy_transactions.version END,content_hash=EXCLUDED.content_hash,payload=EXCLUDED.payload RETURNING version`, memory.UserID, id, itemID, nullText(row, "accountId"), nullText(row, "description"), amount, nullText(row, "currencyCode"), nullText(row, "date"), nullText(row, "status"), timestamp(row, "updatedAt"), now, hash, b).Scan(&version)
		effective = nullText(row, "date")
	case "investment":
		balance, _ := decimal(row, "balance")
		quantity, _ := decimal(row, "quantity")
		unit, _ := decimal(row, "unitValue")
		err = tx.QueryRow(ctx, `INSERT INTO pluggy_investments(user_id,external_id,item_id,name,investment_type,balance,quantity,unit_price,currency,price_currency,effective_at,source_updated_at,observed_at,content_hash,payload) VALUES($1,$2,$3,$4,$5,$6::text::numeric,$7::text::numeric,$8::text::numeric,$9,$10,$11,$12,$13,$14,$15) ON CONFLICT(user_id,external_id) DO UPDATE SET item_id=EXCLUDED.item_id,name=EXCLUDED.name,investment_type=EXCLUDED.investment_type,balance=EXCLUDED.balance,quantity=EXCLUDED.quantity,unit_price=EXCLUDED.unit_price,currency=EXCLUDED.currency,price_currency=EXCLUDED.price_currency,effective_at=EXCLUDED.effective_at,source_updated_at=EXCLUDED.source_updated_at,observed_at=EXCLUDED.observed_at,active=true,version=CASE WHEN pluggy_investments.content_hash<>EXCLUDED.content_hash OR pluggy_investments.item_id<>EXCLUDED.item_id THEN pluggy_investments.version+1 ELSE pluggy_investments.version END,content_hash=EXCLUDED.content_hash,payload=EXCLUDED.payload RETURNING version`, memory.UserID, id, itemID, nullText(row, "name"), nullText(row, "type"), balance, quantity, unit, nullText(row, "currencyCode"), nullText(row, "currencyCode"), nullText(row, "date"), timestamp(row, "updatedAt"), now, hash, b).Scan(&version)
		effective = nullText(row, "date")
	default:
		return ErrInvalid
	}
	if err != nil {
		return err
	}
	return saveSnapshot(ctx, tx, itemID, kind, id, version, hash, b, effective, now)
}
func saveSnapshot(ctx context.Context, tx pgx.Tx, itemID, kind, id string, version int, hash string, payload []byte, effective any, now time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO pluggy_snapshots(user_id,item_id,kind,external_id,version,content_hash,payload,effective_at,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT DO NOTHING`, memory.UserID, itemID, kind, id, version, hash, payload, effective, now)
	return err
}
func contentHash(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func scrub(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := Object{}
		for key, entry := range v {
			normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
			switch normalized {
			case "apikey", "accesstoken", "refreshtoken", "clientsecret", "password", "credentials", "authorization", "token", "parameter", "useraction", "webhookurl":
				continue
			}
			out[key] = scrub(entry)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, entry := range v {
			out[i] = scrub(entry)
		}
		return out
	default:
		return value
	}
}
