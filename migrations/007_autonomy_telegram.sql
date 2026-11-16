ALTER TABLE telegram_outbox ADD COLUMN IF NOT EXISTS id bigserial;
CREATE UNIQUE INDEX IF NOT EXISTS telegram_outbox_receipt_unique ON telegram_outbox(bot_id,update_id);
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='telegram_outbox'::regclass AND contype='p' AND pg_get_constraintdef(oid)<>'PRIMARY KEY (id)') THEN
  ALTER TABLE telegram_outbox DROP CONSTRAINT telegram_outbox_pkey;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='telegram_outbox'::regclass AND contype='p') THEN
  ALTER TABLE telegram_outbox ADD CONSTRAINT telegram_outbox_pkey PRIMARY KEY(id);
 END IF;
END $$;
ALTER TABLE telegram_outbox ALTER COLUMN update_id DROP NOT NULL;
ALTER TABLE telegram_outbox ALTER COLUMN incoming_event_id DROP NOT NULL;
ALTER TABLE telegram_outbox ADD COLUMN IF NOT EXISTS notification_id uuid UNIQUE REFERENCES autonomy_notices(id);
ALTER TABLE telegram_outbox ADD COLUMN IF NOT EXISTS accepted_user_id bigint CHECK(accepted_user_id>0);
ALTER TABLE telegram_outbox ADD COLUMN IF NOT EXISTS accepted_chat_id bigint CHECK(accepted_chat_id>0);
ALTER TABLE telegram_outbox ADD COLUMN IF NOT EXISTS notice_epoch timestamptz;
ALTER TABLE telegram_outbox ADD COLUMN IF NOT EXISTS identity_epoch timestamptz;
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='telegram_outbox'::regclass AND conname='telegram_outbox_source') THEN
  ALTER TABLE telegram_outbox ADD CONSTRAINT telegram_outbox_source CHECK(
   (notification_id IS NULL AND update_id IS NOT NULL AND incoming_event_id IS NOT NULL) OR
   (notification_id IS NOT NULL AND update_id IS NULL AND incoming_event_id IS NULL AND accepted_user_id IS NOT NULL AND accepted_chat_id IS NOT NULL AND notice_epoch IS NOT NULL AND identity_epoch IS NOT NULL)
  );
 END IF;
END $$;
CREATE TABLE IF NOT EXISTS telegram_autonomy_identity(
 user_id uuid PRIMARY KEY, bot_id bigint NOT NULL CHECK(bot_id>0),
 accepted_user_id bigint NOT NULL CHECK(accepted_user_id>0), accepted_chat_id bigint NOT NULL CHECK(accepted_chat_id>0),
 valid_from timestamptz NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO schema_migrations(version) VALUES(7) ON CONFLICT DO NOTHING;
