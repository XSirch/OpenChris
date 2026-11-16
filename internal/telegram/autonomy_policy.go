package telegram

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"openchris/conselho/internal/finance"
	"openchris/conselho/internal/memory"
)

type autonomyNotice struct {
	ID, RunID, Kind, State, Body, FinancialRevision     string
	CreatedAt, RunCreatedAt, NoticeEpoch, IdentityEpoch time.Time
	CompletedAt                                         *time.Time
	Paused, ArtifactValid                               bool
	FinancialContextChanged                             bool
	BotID, UserID, ChatID                               int64
}

func loadAutonomyNotice(ctx context.Context, tx pgx.Tx, id string) (autonomyNotice, error) {
	var notice autonomyNotice
	err := tx.QueryRow(ctx, `SELECT n.id::text,coalesce(r.id::text,''),n.kind,n.delivery_state,n.created_at,coalesce(r.created_at,n.created_at),r.completed_at,a.paused,a.notice_epoch,i.valid_from,i.bot_id,i.accepted_user_id,i.accepted_chat_id,
 CASE WHEN n.kind='summary' THEN r.status IN ('complete','await_data') AND (SELECT count(*) FROM autonomy_tasks t WHERE t.run_id=r.id AND t.status='complete' AND t.result IS NOT NULL)=5 AND EXISTS(SELECT 1 FROM autonomy_strategy_versions v WHERE v.run_id=r.id)
 WHEN n.kind='alert' THEN r.status IN ('partial','stale') AND r.error_code<>'' WHEN n.kind='daily' THEN n.body<>'' AND n.financial_revision<>'' ELSE false END,n.body,n.financial_revision
 FROM autonomy_notices n LEFT JOIN autonomy_runs r ON r.id=n.run_id AND r.user_id=n.user_id JOIN autonomy_state a ON a.user_id=n.user_id JOIN telegram_autonomy_identity i ON i.user_id=n.user_id WHERE n.id::text=$1 AND n.user_id=$2 FOR UPDATE OF n`, id, memory.UserID).Scan(&notice.ID, &notice.RunID, &notice.Kind, &notice.State, &notice.CreatedAt, &notice.RunCreatedAt, &notice.CompletedAt, &notice.Paused, &notice.NoticeEpoch, &notice.IdentityEpoch, &notice.BotID, &notice.UserID, &notice.ChatID, &notice.ArtifactValid, &notice.Body, &notice.FinancialRevision)
	return notice, err
}

func (s *Service) loadCurrentNotice(ctx context.Context, tx pgx.Tx, id string) (autonomyNotice, error) {
	notice, err := loadAutonomyNotice(ctx, tx, id)
	if err == nil && notice.Kind == "daily" {
		personal, readErr := finance.ReadPersonalFinance(ctx, tx, memory.UserID, s.now())
		if readErr != nil {
			return notice, readErr
		}
		notice.FinancialContextChanged = notice.ArtifactValid && notice.FinancialRevision != personal.Revision
		notice.ArtifactValid = notice.ArtifactValid && !notice.FinancialContextChanged
	}
	return notice, err
}

func (s *Service) autonomyNoticeAllowed(notice autonomyNotice) (allowed, quiet bool) {
	if !s.cfg.AutonomyEnabled || notice.Paused || !notice.ArtifactValid || notice.BotID != s.botID || notice.UserID != s.cfg.AllowedUserID || notice.ChatID != s.cfg.AllowedChatID {
		return false, false
	}
	at := s.now()
	if notice.CreatedAt.Before(notice.NoticeEpoch) || notice.CreatedAt.Before(notice.IdentityEpoch) || notice.CreatedAt.After(at) {
		return false, false
	}
	if notice.Kind != "daily" && (notice.CompletedAt == nil || notice.CompletedAt.Before(notice.NoticeEpoch) || notice.RunCreatedAt.Before(notice.IdentityEpoch) || notice.CompletedAt.Before(notice.RunCreatedAt) || notice.CreatedAt.Before(*notice.CompletedAt)) {
		return false, false
	}
	location, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return false, false
	}
	local := at.In(location)
	if notice.CreatedAt.In(location).Format("2006-01-02") != local.Format("2006-01-02") {
		return false, false
	}
	return true, local.Hour() < 8 || local.Hour() >= 22
}

func (s *Service) cancellationCode(notice autonomyNotice) string {
	if notice.FinancialContextChanged {
		notice.ArtifactValid = true
		if allowed, _ := s.autonomyNoticeAllowed(notice); allowed {
			return "financial_context_changed"
		}
	}
	return "autonomy_notice_revoked"
}

func cancelAutonomyNotice(ctx context.Context, tx pgx.Tx, id string, reasons ...string) error {
	code := "autonomy_notice_revoked"
	if len(reasons) > 0 && reasons[0] == "financial_context_changed" {
		code = reasons[0]
	}
	_, err := tx.Exec(ctx, `UPDATE telegram_outbox SET status='failed',error_code=$2 WHERE notification_id::text=$1 AND status IN ('pending','sending')`, id, code)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE autonomy_notices SET delivery_state='cancelled',cancellation_code=$2 WHERE id::text=$1 AND delivery_state IN ('pending','queued')`, id, code)
	}
	return err
}

func (s *Service) revokeAutonomyNotices(ctx context.Context) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, persistenceError(ctx, err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, writerLock); err != nil {
		return false, persistenceError(ctx, err)
	}
	rows, err := tx.Query(ctx, `SELECT o.notification_id::text,o.bot_id,o.chat_id,o.accepted_user_id,o.accepted_chat_id,o.body,o.notice_epoch,o.identity_epoch FROM telegram_outbox o JOIN autonomy_notices n ON n.id=o.notification_id WHERE n.user_id=$1 AND o.status='pending' ORDER BY o.id FOR UPDATE OF o SKIP LOCKED LIMIT 100`, memory.UserID)
	if err != nil {
		return false, persistenceError(ctx, err)
	}
	type pending struct {
		id, body                      string
		bot, chat, user, acceptedChat int64
		noticeEpoch, identityEpoch    time.Time
	}
	var items []pending
	for rows.Next() {
		var item pending
		if err = rows.Scan(&item.id, &item.bot, &item.chat, &item.user, &item.acceptedChat, &item.body, &item.noticeEpoch, &item.identityEpoch); err != nil {
			break
		}
		items = append(items, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	changed := false
	for _, item := range items {
		if err != nil {
			break
		}
		notice, loadErr := s.loadCurrentNotice(ctx, tx, item.id)
		if loadErr != nil && !errors.Is(loadErr, pgx.ErrNoRows) {
			err = loadErr
			break
		}
		allowed, _ := s.autonomyNoticeAllowed(notice)
		if loadErr != nil || !allowed || notice.State != "queued" || item.bot != s.botID || item.chat != s.cfg.AllowedChatID || item.user != s.cfg.AllowedUserID || item.acceptedChat != s.cfg.AllowedChatID || !item.noticeEpoch.Equal(notice.NoticeEpoch) || !item.identityEpoch.Equal(notice.IdentityEpoch) || item.body != s.noticeText(notice) {
			code := "autonomy_notice_revoked"
			if loadErr == nil && notice.State == "queued" && item.bot == s.botID && item.chat == s.cfg.AllowedChatID && item.user == s.cfg.AllowedUserID && item.acceptedChat == s.cfg.AllowedChatID && item.noticeEpoch.Equal(notice.NoticeEpoch) && item.identityEpoch.Equal(notice.IdentityEpoch) && item.body == s.noticeText(notice) {
				code = s.cancellationCode(notice)
			}
			err = cancelAutonomyNotice(ctx, tx, item.id, code)
			changed = true
		}
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return changed, persistenceError(ctx, err)
}

func (s *Service) sendAutonomyClaimed(ctx context.Context, item delivery) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return persistenceError(ctx, err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, writerLock); err != nil {
		return persistenceError(ctx, err)
	}
	var body string
	var bot, chat, user, acceptedChat int64
	var noticeEpoch, identityEpoch time.Time
	err = tx.QueryRow(ctx, `SELECT body,bot_id,chat_id,accepted_user_id,accepted_chat_id,notice_epoch,identity_epoch FROM telegram_outbox WHERE id=$1 AND notification_id::text=$2 AND status='sending' FOR UPDATE`, item.outboxID, item.notificationID).Scan(&body, &bot, &chat, &user, &acceptedChat, &noticeEpoch, &identityEpoch)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return persistenceError(ctx, err)
	}
	notice, err := s.loadCurrentNotice(ctx, tx, item.notificationID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return persistenceError(ctx, err)
	}
	allowed, quiet := s.autonomyNoticeAllowed(notice)
	if err != nil || !allowed || notice.State != "queued" || bot != s.botID || chat != s.cfg.AllowedChatID || user != s.cfg.AllowedUserID || acceptedChat != s.cfg.AllowedChatID || !noticeEpoch.Equal(notice.NoticeEpoch) || !identityEpoch.Equal(notice.IdentityEpoch) || body != s.noticeText(notice) || item.body != body || item.chatID != chat || item.acceptedUserID != user || item.acceptedChatID != acceptedChat {
		code := "autonomy_notice_revoked"
		if err == nil && notice.State == "queued" && bot == s.botID && chat == s.cfg.AllowedChatID && user == s.cfg.AllowedUserID && acceptedChat == s.cfg.AllowedChatID && noticeEpoch.Equal(notice.NoticeEpoch) && identityEpoch.Equal(notice.IdentityEpoch) && body == s.noticeText(notice) && item.body == body && item.chatID == chat && item.acceptedUserID == user && item.acceptedChatID == acceptedChat {
			code = s.cancellationCode(notice)
		}
		err = cancelAutonomyNotice(ctx, tx, item.notificationID, code)
		if err == nil {
			err = tx.Commit(ctx)
		}
		return persistenceError(ctx, err)
	}
	if quiet {
		_, err = tx.Exec(ctx, `UPDATE telegram_outbox SET status='pending',attempts=greatest(attempts-1,0),claimed_at=NULL WHERE id=$1 AND status='sending'`, item.outboxID)
		if err == nil {
			err = tx.Commit(ctx)
		}
		return persistenceError(ctx, err)
	}
	if err = tx.Commit(ctx); err != nil {
		return persistenceError(ctx, err)
	}
	messageID, sendErr := s.client.SendMessage(ctx, chat, body)
	return s.completeAutonomyDelivery(ctx, item, messageID, sendErr)
}
