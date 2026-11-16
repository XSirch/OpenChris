ALTER TABLE autonomy_runs ADD COLUMN IF NOT EXISTS reserved_calls integer NOT NULL DEFAULT 0 CHECK (reserved_calls >= 0 AND reserved_calls <= 10);
ALTER TABLE autonomy_runs ADD COLUMN IF NOT EXISTS quota_day date;
ALTER TABLE autonomy_state ADD COLUMN IF NOT EXISTS budget_wait_until timestamptz;
ALTER TABLE autonomy_notices ADD COLUMN IF NOT EXISTS body text NOT NULL DEFAULT '' CHECK (octet_length(body) <= 4096);
ALTER TABLE autonomy_notices ADD COLUMN IF NOT EXISTS financial_revision text NOT NULL DEFAULT '';
ALTER TABLE autonomy_notices ADD COLUMN IF NOT EXISTS cancellation_code text NOT NULL DEFAULT '';
ALTER TABLE autonomy_notices DROP CONSTRAINT IF EXISTS autonomy_notices_kind_check;
ALTER TABLE autonomy_notices ADD CONSTRAINT autonomy_notices_kind_check CHECK (kind IN ('summary','alert','daily'));
-- The daily digest replaces the previous twice-daily delivery policy.
UPDATE autonomy_notices SET delivery_state='cancelled' WHERE kind IN ('summary','alert') AND delivery_state IN ('pending','queued');
UPDATE telegram_outbox SET status='failed',error_code='autonomy_notice_revoked' WHERE status='pending' AND notification_id IN (SELECT id FROM autonomy_notices WHERE kind IN ('summary','alert') AND delivery_state='cancelled');
INSERT INTO schema_migrations(version) VALUES(9) ON CONFLICT DO NOTHING;
