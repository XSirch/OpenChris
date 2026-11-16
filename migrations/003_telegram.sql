CREATE TABLE IF NOT EXISTS telegram_inbox(
 bot_id bigint NOT NULL CHECK(bot_id > 0),
 update_id bigint NOT NULL CHECK(update_id >= 0),
 payload_hash text NOT NULL CHECK(payload_hash ~ '^[0-9a-f]{64}$'),
 incoming_event_id bigint NOT NULL UNIQUE REFERENCES conversation_events(id),
 accepted_user_id bigint CHECK(accepted_user_id > 0),
 accepted_chat_id bigint CHECK(accepted_chat_id > 0),
 command text NOT NULL CHECK(command IN ('','/start','/ajuda','/resumo','/gastos','/patrimonio','/memoria')),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','queued','complete','failed','unknown')),
 prepare_attempts integer NOT NULL DEFAULT 0 CHECK(prepare_attempts BETWEEN 0 AND 5),
 next_run_at timestamptz NOT NULL DEFAULT now(),
 error_code text,
 accepted_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(bot_id,update_id)
);
-- Existing receipts have no proven sender identity. Preserve them as NULL;
-- workers must revoke them instead of assigning the current allowlist.
ALTER TABLE telegram_inbox ADD COLUMN IF NOT EXISTS accepted_user_id bigint CHECK(accepted_user_id > 0);
ALTER TABLE telegram_inbox ADD COLUMN IF NOT EXISTS accepted_chat_id bigint CHECK(accepted_chat_id > 0);
CREATE INDEX IF NOT EXISTS telegram_inbox_pending ON telegram_inbox(bot_id,next_run_at,update_id) WHERE status='pending';

CREATE TABLE IF NOT EXISTS telegram_outbox(
 bot_id bigint NOT NULL,
 update_id bigint NOT NULL,
 incoming_event_id bigint NOT NULL UNIQUE REFERENCES conversation_events(id),
 chat_id bigint NOT NULL CHECK(chat_id > 0),
 body text NOT NULL CHECK(char_length(body) BETWEEN 1 AND 4096),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','sending','sent','failed','unknown')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 5),
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 claimed_at timestamptz,
 sent_at timestamptz,
 telegram_message_id bigint,
 error_code text,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(bot_id,update_id),
 FOREIGN KEY(bot_id,update_id) REFERENCES telegram_inbox(bot_id,update_id)
);
CREATE INDEX IF NOT EXISTS telegram_outbox_pending ON telegram_outbox(bot_id,next_attempt_at,update_id) WHERE status='pending';
INSERT INTO schema_migrations(version) VALUES(3) ON CONFLICT DO NOTHING;
