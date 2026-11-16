package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"openchris/conselho/internal/memory"
)

const registeredReply = "Mensagem registrada. Consulte o dashboard. IA não configurada."
const helpReply = "Use /resumo, /gastos, /patrimonio ou /memoria para consultar totais. O histórico completo está no dashboard. IA não configurada."

func (s *Service) buildReply(ctx context.Context, command string) (string, error) {
	switch command {
	case "":
		return registeredReply, nil
	case "/start", "/ajuda":
		if s.analysis != nil && s.analysis.Enabled() {
			return "CONSELHO privado. Envie sua pergunta para análise no dashboard; o resultado chega aqui como aviso e link. Comandos: /resumo, /gastos, /patrimonio e /memoria.", nil
		}
		return helpReply, nil
	case "/resumo", "/gastos", "/patrimonio", "/memoria":
		if s.reply == nil {
			return "Consulte o dashboard para acessar seu resumo. IA não configurada.", nil
		}
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		text, err := s.reply(bounded, command)
		if err != nil {
			return "", errors.New("telegram reply unavailable")
		}
		if strings.TrimSpace(text) == "" || !utf8.ValidString(text) || strings.ContainsRune(text, 0) || utf8.RuneCountInString(text) > 4096 {
			return "", errors.New("telegram reply invalid")
		}
		return text, nil
	default:
		return registeredReply, nil
	}
}

func retryDelay(attempt int, retryAfter time.Duration) time.Duration {
	delay := time.Second * time.Duration(1<<uint(attempt-1))
	if delay > 5*time.Minute {
		delay = 5 * time.Minute
	}
	if retryAfter > time.Hour {
		retryAfter = time.Hour
	}
	if retryAfter > delay {
		delay = retryAfter
	}
	return delay
}

func (s *Service) prepareOne(ctx context.Context) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, persistenceError(ctx, err)
	}
	defer tx.Rollback(ctx)
	var updateID, eventID int64
	var command string
	var attempts int
	err = tx.QueryRow(ctx, `SELECT update_id,incoming_event_id,command,prepare_attempts FROM telegram_inbox WHERE bot_id=$1 AND accepted_user_id=$2 AND accepted_chat_id=$3 AND status='pending' AND next_run_at<=clock_timestamp() ORDER BY update_id FOR UPDATE SKIP LOCKED LIMIT 1`, s.botID, s.cfg.AllowedUserID, s.cfg.AllowedChatID).Scan(&updateID, &eventID, &command, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, persistenceError(ctx, err)
	}
	var text string
	var buildErr error
	var analysisJob bool
	if s.analysis != nil {
		buildErr = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM council_jobs WHERE event_id=$1)`, eventID).Scan(&analysisJob)
	}
	if buildErr == nil && analysisJob {
		var ready bool
		text, ready, buildErr = s.analysis.TelegramReply(ctx, tx, eventID)
		if buildErr == nil && !ready {
			return false, nil
		}
	} else if buildErr == nil {
		text, buildErr = s.buildReply(ctx, command)
	}
	if buildErr != nil {
		attempts++
		status := "pending"
		if attempts >= 5 {
			status = "failed"
		}
		_, err = tx.Exec(ctx, `UPDATE telegram_inbox SET prepare_attempts=$3,status=$4,next_run_at=clock_timestamp()+($5 * interval '1 second'),error_code='reply_unavailable' WHERE bot_id=$1 AND update_id=$2`, s.botID, updateID, attempts, status, retryDelay(attempts, 0).Seconds())
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO telegram_outbox(bot_id,update_id,incoming_event_id,chat_id,body) VALUES($1,$2,$3,$4,$5)`, s.botID, updateID, eventID, s.cfg.AllowedChatID, text)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE telegram_inbox SET status='queued',error_code=NULL WHERE bot_id=$1 AND update_id=$2`, s.botID, updateID)
		}
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return true, persistenceError(ctx, err)
}

type delivery struct {
	outboxID                       int64
	notificationID                 string
	updateID, eventID, chatID      int64
	acceptedUserID, acceptedChatID int64
	body                           string
	attempts                       int
}

func (s *Service) claimOne(ctx context.Context) (delivery, bool, error) {
	var item delivery
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return item, false, persistenceError(ctx, err)
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `SELECT o.update_id,o.incoming_event_id,o.chat_id,o.body,o.attempts,i.accepted_user_id,i.accepted_chat_id FROM telegram_outbox o JOIN telegram_inbox i ON i.bot_id=o.bot_id AND i.update_id=o.update_id AND i.incoming_event_id=o.incoming_event_id WHERE o.bot_id=$1 AND o.chat_id=$3 AND i.accepted_user_id=$2 AND i.accepted_chat_id=$3 AND i.status='queued' AND o.status='pending' AND o.attempts<5 AND o.next_attempt_at<=clock_timestamp() ORDER BY o.update_id FOR UPDATE OF o SKIP LOCKED LIMIT 1`, s.botID, s.cfg.AllowedUserID, s.cfg.AllowedChatID).Scan(&item.updateID, &item.eventID, &item.chatID, &item.body, &item.attempts, &item.acceptedUserID, &item.acceptedChatID)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, false, nil
	}
	if err != nil {
		return item, false, persistenceError(ctx, err)
	}
	item.attempts++
	_, err = tx.Exec(ctx, `UPDATE telegram_outbox SET status='sending',attempts=$3,claimed_at=clock_timestamp(),error_code=NULL WHERE bot_id=$1 AND update_id=$2`, s.botID, item.updateID, item.attempts)
	if err == nil {
		err = tx.Commit(ctx)
	}
	return item, err == nil, persistenceError(ctx, err)
}

func (s *Service) completeDelivery(ctx context.Context, item delivery, messageID int64, sendErr error) error {
	if item.notificationID != "" {
		return s.completeAutonomyDelivery(ctx, item, messageID, sendErr)
	}
	status, errorCode := "sent", ""
	var delay time.Duration
	if sendErr != nil {
		var provider *APIError
		if errors.Is(sendErr, ErrUnauthorized) {
			status, errorCode = "failed", "authorization_revoked"
		} else if !errors.As(sendErr, &provider) || provider.Ambiguous {
			status, errorCode = "unknown", "delivery_unknown"
		} else if (provider.Code == 429 || provider.Code >= 500) && item.attempts < 5 {
			status, errorCode = "pending", fmt.Sprintf("api_%d", provider.Code)
			delay = retryDelay(item.attempts, provider.RetryAfter)
		} else {
			status, errorCode = "failed", fmt.Sprintf("api_%d", provider.Code)
		}
	}
	// Persist the result even when cancellation interrupted the request. If this
	// write also fails, the durable sending claim later becomes unknown.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	tx, err := s.db.Begin(finishCtx)
	if err != nil {
		return persistenceError(finishCtx, err)
	}
	defer tx.Rollback(finishCtx)
	if _, err = tx.Exec(finishCtx, `SELECT pg_advisory_xact_lock($1)`, writerLock); err != nil {
		return persistenceError(finishCtx, err)
	}
	_, err = tx.Exec(finishCtx, `UPDATE telegram_outbox SET status=$3,error_code=NULLIF($4,''),next_attempt_at=clock_timestamp()+($5 * interval '1 second'),telegram_message_id=CASE WHEN $3='sent' THEN $6::bigint ELSE NULL END,sent_at=CASE WHEN $3='sent' THEN clock_timestamp() ELSE NULL END WHERE bot_id=$1 AND update_id=$2 AND status='sending'`, s.botID, item.updateID, status, errorCode, delay.Seconds(), messageID)
	if err == nil && status != "pending" {
		inboxStatus := status
		if status == "sent" {
			inboxStatus = "complete"
		}
		_, err = tx.Exec(finishCtx, `UPDATE telegram_inbox SET status=$3,error_code=NULLIF($4,'') WHERE bot_id=$1 AND update_id=$2`, s.botID, item.updateID, inboxStatus, errorCode)
		if err == nil && status == "sent" {
			var outputID int64
			err = tx.QueryRow(finishCtx, `INSERT INTO conversation_events(user_id,channel,external_key,direction,body) VALUES($1,'telegram',$2,'outgoing',$3) ON CONFLICT(user_id,channel,external_key,direction) DO UPDATE SET body=conversation_events.body RETURNING id`, memory.UserID, telegramKey(s.botID, item.updateID), item.body).Scan(&outputID)
			if err == nil {
				_, err = tx.Exec(finishCtx, `UPDATE conversation_threads SET state='complete',last_output_event_id=$3,updated_at=clock_timestamp() WHERE user_id=$1 AND channel='telegram' AND last_input_event_id=$2`, memory.UserID, item.eventID, outputID)
			}
		}
		if err == nil {
			_, err = tx.Exec(finishCtx, `UPDATE episodes SET outcome=$2 WHERE source_event_id=$1`, item.eventID, "telegram_"+status)
		}
		if err == nil {
			err = emit(finishCtx, tx, "telegram.delivery_"+status, map[string]any{"source_event_id": item.eventID, "channel": "telegram", "state": status})
		}
	}
	if err == nil {
		err = tx.Commit(finishCtx)
	}
	return persistenceError(finishCtx, err)
}

// The persisted receipt identifies the user who authorized this particular
// response. A configuration change never retargets already accepted work.
func (s *Service) revokeUnauthorized(ctx context.Context) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, persistenceError(ctx, err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, writerLock); err != nil {
		return false, persistenceError(ctx, err)
	}
	rows, err := tx.Query(ctx, `SELECT i.update_id,i.incoming_event_id FROM telegram_inbox i WHERE i.bot_id=$1 AND i.status IN ('pending','queued') AND (i.accepted_user_id IS DISTINCT FROM $2::bigint OR i.accepted_chat_id IS DISTINCT FROM $3::bigint OR EXISTS(SELECT 1 FROM telegram_outbox o WHERE o.bot_id=i.bot_id AND o.update_id=i.update_id AND o.status='pending' AND o.chat_id IS DISTINCT FROM $3::bigint)) ORDER BY i.update_id FOR UPDATE OF i SKIP LOCKED LIMIT 100`, s.botID, s.cfg.AllowedUserID, s.cfg.AllowedChatID)
	if err != nil {
		return false, persistenceError(ctx, err)
	}
	var items []delivery
	for rows.Next() {
		var item delivery
		if err = rows.Scan(&item.updateID, &item.eventID); err != nil {
			break
		}
		items = append(items, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	for _, item := range items {
		if err != nil {
			break
		}
		_, err = tx.Exec(ctx, `UPDATE telegram_inbox SET status='failed',error_code='authorization_revoked' WHERE bot_id=$1 AND update_id=$2`, s.botID, item.updateID)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE telegram_outbox SET status='failed',error_code='authorization_revoked' WHERE bot_id=$1 AND update_id=$2 AND status='pending'`, s.botID, item.updateID)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE episodes SET outcome='telegram_authorization_revoked' WHERE source_event_id=$1`, item.eventID)
		}
		if err == nil {
			err = emit(ctx, tx, "telegram.delivery_failed", map[string]any{"source_event_id": item.eventID, "channel": "telegram", "state": "failed", "error_code": "authorization_revoked"})
		}
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return len(items) > 0, persistenceError(ctx, err)
}

func (s *Service) recoverUnknown(ctx context.Context) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, persistenceError(ctx, err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, writerLock); err != nil {
		return false, persistenceError(ctx, err)
	}
	rows, err := tx.Query(ctx, `UPDATE telegram_outbox SET status='unknown',error_code='delivery_unknown' WHERE bot_id=$1 AND notification_id IS NULL AND status='sending' AND claimed_at<clock_timestamp()-interval '60 seconds' RETURNING update_id,incoming_event_id`, s.botID)
	if err != nil {
		return false, persistenceError(ctx, err)
	}
	var items []delivery
	for rows.Next() {
		var item delivery
		if err = rows.Scan(&item.updateID, &item.eventID); err != nil {
			break
		}
		items = append(items, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	for _, item := range items {
		if err != nil {
			break
		}
		_, err = tx.Exec(ctx, `UPDATE telegram_inbox SET status='unknown',error_code='delivery_unknown' WHERE bot_id=$1 AND update_id=$2`, s.botID, item.updateID)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE episodes SET outcome='telegram_unknown' WHERE source_event_id=$1`, item.eventID)
		}
		if err == nil {
			err = emit(ctx, tx, "telegram.delivery_unknown", map[string]any{"source_event_id": item.eventID, "channel": "telegram", "state": "unknown"})
		}
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return len(items) > 0, persistenceError(ctx, err)
}

// ProcessOne uses persisted claims and SKIP LOCKED. Telegram sendMessage has no
// idempotency key, so delivery is never advertised as exactly once.
func (s *Service) ProcessOne(ctx context.Context) (bool, error) {
	if !s.cfg.Enabled {
		return false, ErrDisabled
	}
	if s.db == nil {
		return false, errors.New("telegram persistence unavailable")
	}
	revoked, err := s.revokeUnauthorized(ctx)
	if err != nil {
		return false, err
	}
	autonomyRevoked, err := s.revokeAutonomyNotices(ctx)
	if err != nil {
		return revoked, err
	}
	revoked = revoked || autonomyRevoked
	recovered, err := s.recoverUnknown(ctx)
	if err != nil {
		return false, err
	}
	autonomyRecovered, err := s.recoverAutonomyUnknown(ctx)
	if err != nil {
		return revoked || recovered, err
	}
	recovered = recovered || autonomyRecovered
	prepared, err := s.prepareOne(ctx)
	if err != nil {
		return revoked || recovered, err
	}
	item, claimed, err := s.claimOne(ctx)
	if err != nil {
		return revoked || prepared || recovered, err
	}
	if !claimed {
		item, claimed, err = s.claimAutonomyOne(ctx)
		if err != nil {
			return revoked || prepared || recovered, err
		}
		if !claimed {
			return revoked || prepared || recovered, nil
		}
	}
	return true, s.sendClaimed(ctx, item)
}

func (s *Service) sendClaimed(ctx context.Context, item delivery) error {
	if item.notificationID != "" {
		return s.sendAutonomyClaimed(ctx, item)
	}
	// Check the current immutable configuration again at the actual HTTP boundary.
	// Query filters alone cannot justify trusting a persisted destination.
	if item.chatID != s.cfg.AllowedChatID || item.acceptedChatID != s.cfg.AllowedChatID || item.acceptedUserID != s.cfg.AllowedUserID {
		return s.completeDelivery(ctx, item, 0, ErrUnauthorized)
	}
	messageID, sendErr := s.client.SendMessage(ctx, item.chatID, item.body)
	return s.completeDelivery(ctx, item, messageID, sendErr)
}

func (s *Service) Run(ctx context.Context, onError func(error)) {
	if !s.cfg.Enabled {
		return
	}
	for ctx.Err() == nil {
		processed, err := s.ProcessOne(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil && onError != nil {
			onError(err)
		}
		if processed && err == nil {
			continue
		}
		delay := 200 * time.Millisecond
		if err != nil {
			delay = time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *Service) Status(ctx context.Context) (map[string]any, error) {
	s.mu.RLock()
	ready := s.setup
	s.mu.RUnlock()
	status := map[string]any{"enabled": s.cfg.Enabled, "configured": s.cfg.Enabled, "webhook_ready": ready, "status": "disabled", "allowed_identity_configured": s.cfg.AllowedUserID > 0 && s.cfg.AllowedChatID > 0, "webhook_url": "", "last_received_at": nil, "last_sent_at": nil, "pending": int64(0), "queued": int64(0), "sent": int64(0), "failed": int64(0), "unknown": int64(0)}
	if !s.cfg.Enabled {
		return status, nil
	}
	if s.db == nil {
		return nil, errors.New("telegram persistence unavailable")
	}
	var pending, queued, sent, failed, unknown int64
	var receivedAt, sentAt *time.Time
	err := s.db.QueryRow(ctx, `SELECT count(*) FILTER(WHERE status='pending'),count(*) FILTER(WHERE status='queued'),count(*) FILTER(WHERE status='complete'),count(*) FILTER(WHERE status='failed'),count(*) FILTER(WHERE status='unknown'),max(accepted_at),(SELECT max(sent_at) FROM telegram_outbox WHERE bot_id=$1) FROM telegram_inbox WHERE bot_id=$1`, s.botID).Scan(&pending, &queued, &sent, &failed, &unknown, &receivedAt, &sentAt)
	if err != nil {
		return nil, persistenceError(ctx, err)
	}
	status["pending"], status["queued"], status["sent"], status["failed"], status["unknown"] = pending+queued, queued, sent, failed, unknown
	status["webhook_url"] = s.cfg.PublicURL
	if receivedAt != nil {
		status["last_received_at"] = *receivedAt
	}
	if sentAt != nil {
		status["last_sent_at"] = *sentAt
	}
	switch {
	case failed+unknown > 0:
		status["status"] = "failed"
	case pending+queued > 0:
		status["status"] = "pending"
	case ready || receivedAt != nil:
		status["status"] = "connected"
	default:
		status["status"] = "waiting_setup"
	}
	return status, nil
}
