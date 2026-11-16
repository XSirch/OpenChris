package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"openchris/conselho/internal/memory"
)

func (s *Service) registerAutonomyIdentity(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `INSERT INTO telegram_autonomy_identity(user_id,bot_id,accepted_user_id,accepted_chat_id) VALUES($1,$2,$3,$4) ON CONFLICT(user_id) DO UPDATE SET bot_id=EXCLUDED.bot_id,accepted_user_id=EXCLUDED.accepted_user_id,accepted_chat_id=EXCLUDED.accepted_chat_id,valid_from=clock_timestamp() WHERE telegram_autonomy_identity.bot_id IS DISTINCT FROM EXCLUDED.bot_id OR telegram_autonomy_identity.accepted_user_id IS DISTINCT FROM EXCLUDED.accepted_user_id OR telegram_autonomy_identity.accepted_chat_id IS DISTINCT FROM EXCLUDED.accepted_chat_id`, memory.UserID, s.botID, s.cfg.AllowedUserID, s.cfg.AllowedChatID)
	return persistenceError(ctx, err)
}

func (s *Service) autonomyNoticeText(runID, kind string) string {
	u, _ := url.Parse(s.cfg.PublicURL)
	u.Path, u.RawPath = "/dashboard", ""
	u.RawQuery = url.Values{"run": {runID}}.Encode()
	text := "Há uma nova rodada autônoma no CONSELHO. Consulte o painel: "
	if kind == "alert" {
		text = "Há uma pendência na operação autônoma do CONSELHO. Consulte o painel: "
	}
	return text + u.String()
}

func (s *Service) noticeText(notice autonomyNotice) string {
	if notice.Kind != "daily" {
		return s.autonomyNoticeText(notice.RunID, notice.Kind)
	}
	u, _ := url.Parse(s.cfg.PublicURL)
	u.Path, u.RawPath, u.RawQuery, u.Fragment = "/reports", "", "", ""
	if notice.RunID != "" {
		u.RawQuery = url.Values{"run": {notice.RunID}}.Encode()
	}
	report := u.String()
	u.Path, u.RawQuery = "/finance/profile", ""
	return notice.Body + "\nRelatório: " + report + "\nPerfil e pendências: " + u.String()
}

func (s *Service) QueueAutonomyNotice(ctx context.Context, noticeID string) error {
	if !s.cfg.Enabled || !s.cfg.AutonomyEnabled {
		return ErrDisabled
	}
	if s.db == nil {
		return errors.New("telegram persistence unavailable")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return persistenceError(ctx, err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, writerLock); err != nil {
		return persistenceError(ctx, err)
	}
	notice, err := s.loadCurrentNotice(ctx, tx, noticeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalid
	}
	if err != nil {
		return persistenceError(ctx, err)
	}
	if notice.State != "pending" {
		return nil
	}
	allowed, quiet := s.autonomyNoticeAllowed(notice)
	if !allowed {
		err = cancelAutonomyNotice(ctx, tx, noticeID, s.cancellationCode(notice))
		if err == nil {
			err = tx.Commit(ctx)
		}
		return persistenceError(ctx, err)
	}
	if quiet {
		return nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO telegram_outbox(bot_id,chat_id,body,notification_id,accepted_user_id,accepted_chat_id,notice_epoch,identity_epoch) VALUES($1,$2,$3,$4,$5,$2,$6,$7) ON CONFLICT(notification_id) DO UPDATE SET body=EXCLUDED.body,status='pending',error_code=NULL,attempts=0,claimed_at=NULL,next_attempt_at=clock_timestamp(),notice_epoch=EXCLUDED.notice_epoch,identity_epoch=EXCLUDED.identity_epoch WHERE telegram_outbox.status='failed' AND telegram_outbox.error_code='financial_context_changed' AND telegram_outbox.bot_id=EXCLUDED.bot_id AND telegram_outbox.chat_id=EXCLUDED.chat_id AND telegram_outbox.accepted_user_id=EXCLUDED.accepted_user_id AND telegram_outbox.accepted_chat_id=EXCLUDED.accepted_chat_id`, s.botID, s.cfg.AllowedChatID, s.noticeText(notice), noticeID, s.cfg.AllowedUserID, notice.NoticeEpoch, notice.IdentityEpoch)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE autonomy_notices SET delivery_state='queued' WHERE id::text=$1`, noticeID)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return persistenceError(ctx, err)
}

func (s *Service) claimAutonomyOne(ctx context.Context) (delivery, bool, error) {
	var item delivery
	if !s.cfg.AutonomyEnabled {
		return item, false, nil
	}
	location, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return item, false, err
	}
	hour := s.now().In(location).Hour()
	if hour < 8 || hour >= 22 {
		return item, false, nil
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return item, false, persistenceError(ctx, err)
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `SELECT o.id,o.notification_id::text,o.chat_id,o.body,o.attempts,o.accepted_user_id,o.accepted_chat_id FROM telegram_outbox o JOIN autonomy_notices n ON n.id=o.notification_id WHERE o.bot_id=$1 AND o.status='pending' AND n.delivery_state='queued' AND o.attempts<5 AND o.next_attempt_at<=clock_timestamp() ORDER BY o.id FOR UPDATE OF o SKIP LOCKED LIMIT 1`, s.botID).Scan(&item.outboxID, &item.notificationID, &item.chatID, &item.body, &item.attempts, &item.acceptedUserID, &item.acceptedChatID)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, false, nil
	}
	if err != nil {
		return item, false, persistenceError(ctx, err)
	}
	item.attempts++
	_, err = tx.Exec(ctx, `UPDATE telegram_outbox SET status='sending',attempts=$2,claimed_at=clock_timestamp(),error_code=NULL WHERE id=$1`, item.outboxID, item.attempts)
	if err == nil {
		err = tx.Commit(ctx)
	}
	return item, err == nil, persistenceError(ctx, err)
}

func (s *Service) completeAutonomyDelivery(ctx context.Context, item delivery, messageID int64, sendErr error) error {
	status, code := "sent", ""
	var delay time.Duration
	if sendErr != nil {
		var provider *APIError
		if errors.Is(sendErr, ErrUnauthorized) {
			status, code = "failed", "authorization_revoked"
		} else if !errors.As(sendErr, &provider) || provider.Ambiguous {
			status, code = "unknown", "delivery_unknown"
		} else if (provider.Code == 429 || provider.Code >= 500) && item.attempts < 5 {
			status, code, delay = "pending", fmt.Sprintf("api_%d", provider.Code), retryDelay(item.attempts, provider.RetryAfter)
		} else {
			status, code = "failed", fmt.Sprintf("api_%d", provider.Code)
		}
	}
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
	updated, err := tx.Exec(finishCtx, `UPDATE telegram_outbox SET status=$2,error_code=NULLIF($3,''),next_attempt_at=clock_timestamp()+($4 * interval '1 second'),telegram_message_id=CASE WHEN $2='sent' THEN $5::bigint ELSE NULL END,sent_at=CASE WHEN $2='sent' THEN clock_timestamp() ELSE NULL END WHERE id=$1 AND status='sending'`, item.outboxID, status, code, delay.Seconds(), messageID)
	if err == nil && updated.RowsAffected() == 1 {
		state := status
		if status == "pending" {
			state = "queued"
		}
		if status == "unknown" {
			state = "failed_unknown"
		}
		_, err = tx.Exec(finishCtx, `UPDATE autonomy_notices SET delivery_state=$2 WHERE id::text=$1`, item.notificationID, state)
		if err == nil && status != "pending" {
			err = emit(finishCtx, tx, "autonomy.notice_"+status, map[string]any{"notice_id": item.notificationID, "channel": "telegram", "state": state})
		}
	}
	if err == nil {
		err = tx.Commit(finishCtx)
	}
	return persistenceError(finishCtx, err)
}

func (s *Service) recoverAutonomyUnknown(ctx context.Context) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, persistenceError(ctx, err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, writerLock); err != nil {
		return false, persistenceError(ctx, err)
	}
	rows, err := tx.Query(ctx, `UPDATE telegram_outbox SET status='unknown',error_code='delivery_unknown' WHERE notification_id IN(SELECT id FROM autonomy_notices WHERE user_id=$1) AND status='sending' AND claimed_at<clock_timestamp()-interval '60 seconds' RETURNING notification_id::text`, memory.UserID)
	if err != nil {
		return false, persistenceError(ctx, err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	for _, id := range ids {
		if err != nil {
			break
		}
		_, err = tx.Exec(ctx, `UPDATE autonomy_notices SET delivery_state='failed_unknown' WHERE id::text=$1 AND delivery_state='queued'`, id)
		if err == nil {
			err = emit(ctx, tx, "autonomy.notice_unknown", map[string]any{"notice_id": id, "channel": "telegram", "state": "failed_unknown"})
		}
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return len(ids) > 0, persistenceError(ctx, err)
}
