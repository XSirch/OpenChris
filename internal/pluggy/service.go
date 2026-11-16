package pluggy

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"openchris/conselho/internal/memory"
)

type Config struct {
	Enabled                                          bool
	ClientID, ClientSecret, WebhookSecret, PublicURL string
	SyncInterval                                     time.Duration
	// Explicit overrides are for httptest. The application never reads these from environment.
	APIBaseURL string
	HTTPClient *http.Client
}

func (cfg Config) Validate() error {
	if !cfg.Enabled {
		return nil
	}
	if strings.TrimSpace(cfg.ClientID) == "" || strings.TrimSpace(cfg.ClientSecret) == "" || strings.TrimSpace(cfg.WebhookSecret) == "" {
		return errors.New("enabled Pluggy integration requires client id, client secret and webhook secret")
	}
	if len(cfg.WebhookSecret) < 32 {
		return errors.New("Pluggy webhook secret must contain at least 32 bytes")
	}
	u, err := url.Parse(cfg.PublicURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("Pluggy webhook URL must be an absolute HTTPS URL without credentials or query")
	}
	if cfg.SyncInterval < 0 {
		return errors.New("Pluggy sync interval must be positive")
	}
	return nil
}

type Service struct {
	db     *pgxpool.Pool
	cfg    Config
	client *Client
}

func New(db *pgxpool.Pool, cfg Config) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.SyncInterval == 0 {
		cfg.SyncInterval = 24 * time.Hour
	}
	options := []ClientOption{}
	if cfg.APIBaseURL != "" {
		options = append(options, WithBaseURL(cfg.APIBaseURL))
	}
	if cfg.HTTPClient != nil {
		options = append(options, WithHTTPClient(cfg.HTTPClient))
	}
	return &Service{db: db, cfg: cfg, client: NewClient(cfg.ClientID, cfg.ClientSecret, options...)}, nil
}
func (s *Service) ready() error {
	if s == nil || !s.cfg.Enabled {
		return ErrDisabled
	}
	return nil
}
func (s *Service) storage() error {
	if err := s.ready(); err != nil {
		return err
	}
	if s.db == nil {
		return errors.New("Pluggy storage unavailable")
	}
	return nil
}
func (s *Service) ConnectToken(ctx context.Context) (string, error) {
	if err := s.ready(); err != nil {
		return "", err
	}
	return s.client.ConnectToken(ctx, memory.UserID)
}
func (s *Service) ConnectInfo(ctx context.Context) (map[string]any, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	connectors, err := s.client.Connectors(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range connectors {
		if strings.EqualFold(strings.ReplaceAll(textField(row, "name"), " ", ""), "MeuPluggy") && row["isSandbox"] != true {
			id, ok := row["id"].(json.Number)
			if !ok {
				return nil, errors.New("MeuPluggy connector id missing")
			}
			return Object{"connectorId": id, "selectedConnectorId": id, "name": "MeuPluggy", "max_connections": 5, "source_update_hours": 24, "token_expires_in": 1800, "personal_use_only": true}, nil
		}
	}
	return nil, errors.New("MeuPluggy connector is not enabled in this application")
}

var financialEvents = []string{"item/created", "item/updated", "item/deleted", "item/error", "item/waiting_user_input", "item/waiting_user_action", "item/login_succeeded", "transactions/created", "transactions/updated", "transactions/deleted"}

func allowedEvent(event string) bool {
	for _, known := range financialEvents {
		if event == known {
			return true
		}
	}
	return false
}
func equalSecret(a, b string) bool {
	ah, bh := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ah[:], bh[:]) == 1
}
func (s *Service) SetupWebhooks(ctx context.Context) error {
	if err := s.ready(); err != nil {
		return err
	}
	rows, err := s.client.pages(ctx, "/webhooks", nil)
	if err != nil {
		return err
	}
	for _, event := range financialEvents {
		found := false
		for _, row := range rows {
			if row["url"] != s.cfg.PublicURL || row["event"] != event {
				continue
			}
			headers, _ := row["headers"].(map[string]any)
			if row["disabledAt"] != nil || !equalSecret(textField(headers, WebhookHeader), s.cfg.WebhookSecret) {
				return errors.New("existing Pluggy webhook is disabled or has a different secret; review its registration")
			}
			found = true
			break
		}
		if !found {
			if _, err = s.client.authenticated(ctx, "POST", "/webhooks", nil, Object{"url": s.cfg.PublicURL, "event": event, "headers": Object{WebhookHeader: s.cfg.WebhookSecret}}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) RegisterItem(ctx context.Context, itemID string) error {
	return s.register(ctx, itemID, true)
}
func (s *Service) register(ctx context.Context, itemID string, explicit bool) error {
	if err := s.storage(); err != nil {
		return err
	}
	if !uuidPattern.MatchString(itemID) {
		return ErrInvalid
	}
	item, err := s.client.Item(ctx, itemID)
	if err != nil {
		return err
	}
	owner := textField(item, "clientUserId")
	if owner != memory.UserID && !(explicit && owner == "") {
		return ErrUnauthorized
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(2026102804)`); err != nil {
		return err
	}
	var existingOwner string
	err = tx.QueryRow(ctx, `SELECT user_id::text FROM pluggy_items WHERE item_id=$1`, itemID).Scan(&existingOwner)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil && existingOwner != memory.UserID {
		return ErrUnauthorized
	}
	meta := safeItem(item)
	b, _ := json.Marshal(meta)
	connector, _ := item["connector"].(map[string]any)
	state, code := itemState(item, time.Now())
	if state == "connected" {
		state = "stale"
	}
	_, err = tx.Exec(ctx, `INSERT INTO pluggy_items(item_id,user_id,connector_id,connector_name,status,provider_status,consent_expires_at,last_source_sync,explicit_owner,metadata,last_error_code) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(item_id) DO UPDATE SET explicit_owner=pluggy_items.explicit_owner OR EXCLUDED.explicit_owner`, itemID, memory.UserID, stringValue(connector["id"]), nullText(connector, "name"), state, nullText(item, "status"), timestamp(item, "consentExpiresAt"), timestamp(item, "lastUpdatedAt"), explicit, b, nilString(code))
	if err != nil {
		return err
	}
	if explicit {
		if _, err = tx.Exec(ctx, `INSERT INTO pluggy_jobs(user_id,item_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, memory.UserID, itemID); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO pluggy_audit(user_id,item_id,action,payload) VALUES($1,$2,'registered',$3)`, memory.UserID, itemID, []byte(fmt.Sprintf(`{"explicit_owner":%t}`, explicit))); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Service) QueueSync(ctx context.Context, itemID string) error {
	if err := s.storage(); err != nil {
		return err
	}
	if !uuidPattern.MatchString(itemID) {
		return ErrInvalid
	}
	tag, err := s.db.Exec(ctx, `INSERT INTO pluggy_jobs(user_id,item_id) SELECT user_id,item_id FROM pluggy_items WHERE user_id=$1 AND item_id=$2 AND superseded_by IS NULL ON CONFLICT DO NOTHING`, memory.UserID, itemID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err = s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pluggy_items WHERE user_id=$1 AND item_id=$2 AND superseded_by IS NULL)`, memory.UserID, itemID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrInvalid
		}
	}
	return nil
}

// HandleWebhook commits the inbox and work atomically; provider reads happen in Run.
func (s *Service) HandleWebhook(ctx context.Context, header string, raw []byte) (bool, error) {
	if err := s.storage(); err != nil {
		return false, err
	}
	if s.cfg.WebhookSecret == "" || !equalSecret(header, s.cfg.WebhookSecret) {
		return false, ErrUnauthorized
	}
	if len(raw) == 0 || len(raw) > 256<<10 || !utf8.Valid(raw) {
		return false, ErrInvalid
	}
	var event struct{ Event, EventID, ItemID, ClientUserID string }
	if json.Unmarshal(raw, &event) != nil || !allowedEvent(event.Event) || !uuidPattern.MatchString(event.EventID) || !uuidPattern.MatchString(event.ItemID) {
		return false, ErrInvalid
	}
	if event.ClientUserID != "" && event.ClientUserID != memory.UserID {
		return false, ErrUnauthorized
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var known bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pluggy_items WHERE user_id=$1 AND item_id=$2)`, memory.UserID, event.ItemID).Scan(&known); err != nil {
		return false, err
	}
	discover := !known && event.Event == "item/created" && event.ClientUserID == memory.UserID
	state := "pending"
	if !known && !discover {
		state = "ignored"
	}
	// Save only routing evidence. Provider URLs and error descriptions are untrusted and unnecessary.
	b, _ := json.Marshal(Object{"event": event.Event, "eventId": event.EventID, "itemId": event.ItemID, "clientUserId": event.ClientUserID})
	tag, err := tx.Exec(ctx, `INSERT INTO pluggy_inbox(event_id,user_id,item_id,event,payload,status) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, event.EventID, memory.UserID, event.ItemID, event.Event, b, state)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return true, tx.Commit(ctx)
	}
	if state == "pending" {
		if _, err = tx.Exec(ctx, `INSERT INTO pluggy_jobs(user_id,item_id,event_id,discover) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, memory.UserID, event.ItemID, event.EventID, discover); err != nil {
			return false, err
		}
	}
	return false, tx.Commit(ctx)
}

func nullText(row Object, key string) any {
	if value, ok := row[key].(string); ok {
		return value
	}
	return nil
}
func nilString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func stringValue(value any) any {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	}
	return nil
}
func timestamp(row Object, key string) *time.Time {
	v, ok := row[key].(string)
	if !ok {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return nil
	}
	return &t
}
func safeItem(item Object) Object {
	result := Object{}
	for _, key := range []string{"id", "clientUserId", "status", "executionStatus", "createdAt", "updatedAt", "lastUpdatedAt", "consentExpiresAt", "products", "statusDetail", "nextAutoSyncAt"} {
		if value, ok := item[key]; ok {
			result[key] = value
		}
	}
	if connector, ok := item["connector"].(map[string]any); ok {
		result["connector"] = Object{"id": connector["id"], "name": connector["name"], "type": connector["type"]}
	}
	if providerError, ok := item["error"].(map[string]any); ok {
		result["error"] = Object{"code": providerError["code"]}
	}
	return scrub(result).(Object)
}
func itemState(item Object, now time.Time) (string, string) {
	if expires := timestamp(item, "consentExpiresAt"); expires != nil && !expires.After(now) {
		return "expired", "USER_AUTHORIZATION_EXPIRED"
	}
	providerError, _ := item["error"].(map[string]any)
	code := textField(providerError, "code")
	switch code {
	case "USER_AUTHORIZATION_EXPIRED":
		return "expired", code
	case "USER_AUTHORIZATION_REVOKED", "CONSENT_REVOKED", "ITEM_DELETED":
		return "revoked", code
	case "USER_AUTHORIZATION_PENDING", "INVALID_CREDENTIALS", "CREDENTIALS_REQUIRED", "USER_INPUT_TIMEOUT":
		return "user_action_required", code
	}
	switch textField(item, "status") {
	case "DELETED", "REVOKED":
		return "revoked", code
	case "WAITING_USER_INPUT", "WAITING_USER_ACTION", "LOGIN_ERROR":
		return "user_action_required", code
	case "UPDATING", "LOGIN_SUCCEEDED", "CREATING":
		return "syncing", code
	case "OUTDATED":
		return "stale", code
	case "ERROR":
		return "failed", code
	case "UPDATED":
		if code != "" {
			return "failed", code
		}
		if status := textField(item, "executionStatus"); status != "" && status != "SUCCESS" {
			return "partial", status
		}
		if details, ok := item["statusDetail"].(map[string]any); ok {
			for _, value := range details {
				if detail, ok := value.(map[string]any); ok {
					status := textField(detail, "status")
					if status == "ERROR" || status == "FAILED" || detail["error"] != nil {
						return "partial", "PRODUCT_INCOMPLETE"
					}
				}
			}
		}
		return "connected", ""
	}
	return "failed", "UNKNOWN_PROVIDER_STATUS"
}
