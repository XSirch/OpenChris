package telegram

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"openchris/conselho/internal/memory"
)

const maxUpdateBytes = 64 << 10
const writerLock = 20261027

// JSON duplicate keys are rejected at every depth, so the authenticated sender
// cannot be interpreted differently by separate decoders or intermediaries.
func readJSON(decoder *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, ErrInvalid
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, ErrInvalid
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := map[string]any{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, ErrInvalid
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, ErrInvalid
			}
			if _, exists := object[key]; exists {
				return nil, ErrInvalid
			}
			value, err := readJSON(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return nil, ErrInvalid
		}
		return object, nil
	case '[':
		array := []any{}
		for decoder.More() {
			value, err := readJSON(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return nil, ErrInvalid
		}
		return array, nil
	default:
		return nil, ErrInvalid
	}
}

type acceptedUpdate struct {
	id                  int64
	text, hash, command string
}

func parseUpdate(cfg Config, raw []byte) (acceptedUpdate, error) {
	var accepted acceptedUpdate
	if len(raw) == 0 || len(raw) > maxUpdateBytes || !utf8.Valid(raw) {
		return accepted, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := readJSON(decoder, 0)
	if err != nil {
		return accepted, ErrInvalid
	}
	if _, err = decoder.Token(); !errors.Is(err, io.EOF) {
		return accepted, ErrInvalid
	}
	object, ok := value.(map[string]any)
	if !ok || len(object) != 2 {
		return accepted, ErrInvalid
	}
	if _, ok = object["update_id"]; !ok {
		return accepted, ErrInvalid
	}
	message, ok := object["message"].(map[string]any)
	if !ok {
		return accepted, ErrInvalid
	}
	for _, key := range []string{"forward_origin", "forward_from", "forward_from_chat", "forward_sender_name", "forward_date", "is_automatic_forward", "sender_chat", "business_connection_id", "via_bot", "edit_date"} {
		if _, exists := message[key]; exists {
			return accepted, ErrInvalid
		}
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return accepted, ErrInvalid
	}
	var update struct {
		ID      *int64 `json:"update_id"`
		Message struct {
			ID   int64  `json:"message_id"`
			Date int64  `json:"date"`
			Text string `json:"text"`
			From *struct {
				ID    int64 `json:"id"`
				IsBot *bool `json:"is_bot"`
			} `json:"from"`
			Chat struct {
				ID   int64  `json:"id"`
				Type string `json:"type"`
			} `json:"chat"`
		} `json:"message"`
	}
	if json.Unmarshal(canonical, &update) != nil || update.ID == nil || *update.ID < 0 || update.Message.ID <= 0 || update.Message.Date <= 0 || update.Message.From == nil || update.Message.From.IsBot == nil {
		return accepted, ErrInvalid
	}
	if *update.Message.From.IsBot || update.Message.From.ID != cfg.AllowedUserID || update.Message.Chat.ID != cfg.AllowedChatID || update.Message.Chat.Type != "private" {
		return accepted, ErrUnauthorized
	}
	text := update.Message.Text
	if strings.TrimSpace(text) == "" || strings.ContainsRune(text, 0) || !utf8.ValidString(text) || utf8.RuneCountInString(text) > 4096 {
		return accepted, ErrInvalid
	}
	hash := sha256.Sum256(canonical)
	accepted = acceptedUpdate{id: *update.ID, text: text, hash: hex.EncodeToString(hash[:])}
	command := strings.Fields(text)[0]
	switch command {
	case "/start", "/ajuda", "/resumo", "/gastos", "/patrimonio", "/memoria":
		accepted.command = command
	}
	return accepted, nil
}

func persistenceError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.New("telegram persistence failed")
}

func telegramKey(botID, updateID int64) string { return fmt.Sprintf("telegram:%d:%d", botID, updateID) }

func emit(ctx context.Context, tx pgx.Tx, kind string, payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO stream_events(user_id,event_type,payload) VALUES($1,$2,$3)`, memory.UserID, kind, data)
	return err
}

// HandleUpdate commits the inbox receipt and raw M0 journal together. It never
// invokes the synthetic memory processor, a language model, or Telegram itself.
func (s *Service) HandleUpdate(ctx context.Context, secretHeader string, raw []byte) (bool, error) {
	if !s.cfg.Enabled {
		return false, ErrDisabled
	}
	if subtle.ConstantTimeCompare([]byte(secretHeader), []byte(s.cfg.WebhookSecret)) != 1 {
		return false, ErrUnauthorized
	}
	accepted, err := parseUpdate(s.cfg, raw)
	if err != nil {
		return false, err
	}
	if s.db == nil {
		return false, persistenceError(ctx, errors.New("database missing"))
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, persistenceError(ctx, err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, writerLock); err != nil {
		return false, persistenceError(ctx, err)
	}
	var original string
	err = tx.QueryRow(ctx, `SELECT payload_hash FROM telegram_inbox WHERE bot_id=$1 AND update_id=$2`, s.botID, accepted.id).Scan(&original)
	if err == nil {
		if original != accepted.hash {
			return false, ErrConflict
		}
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, persistenceError(ctx, err)
	}
	var eventID int64
	err = tx.QueryRow(ctx, `INSERT INTO conversation_events(user_id,channel,external_key,direction,body) VALUES($1,'telegram',$2,'incoming',$3) RETURNING id`, memory.UserID, telegramKey(s.botID, accepted.id), accepted.text).Scan(&eventID)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO telegram_inbox(bot_id,update_id,payload_hash,incoming_event_id,command,accepted_user_id,accepted_chat_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, s.botID, accepted.id, accepted.hash, eventID, accepted.command, s.cfg.AllowedUserID, s.cfg.AllowedChatID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO conversation_threads(user_id,channel,last_input_event_id,state) VALUES($1,'telegram',$2,'pending') ON CONFLICT(user_id,channel) DO UPDATE SET last_input_event_id=EXCLUDED.last_input_event_id,state='pending',updated_at=clock_timestamp()`, memory.UserID, eventID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO episodes(user_id,source_event_id,channel,body,outcome) VALUES($1,$2,'telegram',$3,'telegram_registered')`, memory.UserID, eventID, accepted.text)
	}
	if err == nil {
		err = emit(ctx, tx, "conversation.received", map[string]any{"source_event_id": eventID, "channel": "telegram", "state": "pending"})
	}
	if err == nil && accepted.command == "" && s.analysis != nil {
		err = s.analysis.EnqueueTx(ctx, tx, eventID, accepted.text)
	}
	if err == nil {
		err = emit(ctx, tx, "memory.episode_saved", map[string]any{"source_event_id": eventID, "channel": "telegram", "state": "complete"})
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return false, persistenceError(ctx, err)
}
