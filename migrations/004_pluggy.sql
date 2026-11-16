CREATE TABLE IF NOT EXISTS pluggy_items(
 item_id uuid PRIMARY KEY,
 user_id uuid NOT NULL,
 connector_id text,
 connector_name text,
 status text NOT NULL CHECK(status IN ('connected','syncing','stale','partial','expired','user_action_required','revoked','failed')),
 provider_status text,
 consent_expires_at timestamptz,
 last_source_sync timestamptz,
 last_synced_at timestamptz,
 last_attempt_at timestamptz,
 next_sync_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 last_error_code text,
 explicit_owner boolean NOT NULL DEFAULT false,
 superseded_by uuid,
 coverage jsonb NOT NULL DEFAULT '{}',
 metadata jsonb NOT NULL DEFAULT '{}',
 version integer NOT NULL DEFAULT 1 CHECK(version>0),
 registered_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 observed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(user_id,item_id)
);
CREATE INDEX IF NOT EXISTS pluggy_items_due ON pluggy_items(user_id,next_sync_at);

CREATE TABLE IF NOT EXISTS pluggy_accounts(
 user_id uuid NOT NULL,
 external_id text NOT NULL,
 item_id uuid NOT NULL,
 name text,
 account_type text,
 subtype text,
 balance numeric,
 currency text,
 source_updated_at timestamptz,
 observed_at timestamptz NOT NULL,
 ingested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 version integer NOT NULL DEFAULT 1 CHECK(version>0),
 content_hash text NOT NULL,
 active boolean NOT NULL DEFAULT true,
 payload jsonb NOT NULL,
 PRIMARY KEY(user_id,external_id),
 FOREIGN KEY(user_id,item_id) REFERENCES pluggy_items(user_id,item_id)
);
CREATE INDEX IF NOT EXISTS pluggy_accounts_item ON pluggy_accounts(user_id,item_id);

CREATE TABLE IF NOT EXISTS pluggy_transactions(
 user_id uuid NOT NULL,
 external_id text NOT NULL,
 item_id uuid NOT NULL,
 account_external_id text,
 description text,
 amount numeric,
 currency text,
 effective_at text,
 provider_status text,
 source_updated_at timestamptz,
 observed_at timestamptz NOT NULL,
 ingested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 version integer NOT NULL DEFAULT 1 CHECK(version>0),
 content_hash text NOT NULL,
 active boolean NOT NULL DEFAULT true,
 payload jsonb NOT NULL,
 PRIMARY KEY(user_id,external_id),
 FOREIGN KEY(user_id,item_id) REFERENCES pluggy_items(user_id,item_id)
);
CREATE INDEX IF NOT EXISTS pluggy_transactions_item ON pluggy_transactions(user_id,item_id);

CREATE TABLE IF NOT EXISTS pluggy_investments(
 user_id uuid NOT NULL,
 external_id text NOT NULL,
 item_id uuid NOT NULL,
 name text,
 investment_type text,
 balance numeric,
 quantity numeric,
 unit_price numeric,
 currency text,
 price_currency text,
 effective_at text,
 source_updated_at timestamptz,
 observed_at timestamptz NOT NULL,
 ingested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 version integer NOT NULL DEFAULT 1 CHECK(version>0),
 content_hash text NOT NULL,
 active boolean NOT NULL DEFAULT true,
 payload jsonb NOT NULL,
 PRIMARY KEY(user_id,external_id),
 FOREIGN KEY(user_id,item_id) REFERENCES pluggy_items(user_id,item_id)
);
CREATE INDEX IF NOT EXISTS pluggy_investments_item ON pluggy_investments(user_id,item_id);

-- A source id keeps its identity when a renewed consent creates a new item.
-- Immutable versions preserve the previous item's evidence and original dates.
CREATE TABLE IF NOT EXISTS pluggy_snapshots(
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 user_id uuid NOT NULL,
 item_id uuid NOT NULL,
 kind text NOT NULL CHECK(kind IN ('item','account','transaction','investment')),
 external_id text NOT NULL,
 version integer NOT NULL CHECK(version>0),
 schema_version integer NOT NULL DEFAULT 1,
 content_hash text NOT NULL,
 payload jsonb NOT NULL,
 effective_at text,
 observed_at timestamptz NOT NULL,
 ingested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(user_id,kind,external_id,version)
);
CREATE INDEX IF NOT EXISTS pluggy_snapshots_item ON pluggy_snapshots(user_id,item_id,id DESC);

CREATE TABLE IF NOT EXISTS pluggy_audit(
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 user_id uuid NOT NULL,
 item_id uuid,
 action text NOT NULL,
 payload jsonb NOT NULL DEFAULT '{}',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE IF NOT EXISTS pluggy_inbox(
 event_id uuid PRIMARY KEY,
 user_id uuid NOT NULL,
 item_id uuid NOT NULL,
 event text NOT NULL,
 payload jsonb NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','processed','ignored','failed')),
 received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 processed_at timestamptz
);

CREATE TABLE IF NOT EXISTS pluggy_jobs(
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 user_id uuid NOT NULL,
 item_id uuid NOT NULL,
 event_id uuid UNIQUE REFERENCES pluggy_inbox(event_id),
 discover boolean NOT NULL DEFAULT false,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','running','complete','failed')),
 attempts integer NOT NULL DEFAULT 0,
 available_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 locked_at timestamptz,
 last_error_code text,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 finished_at timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS pluggy_one_periodic_job ON pluggy_jobs(user_id,item_id) WHERE event_id IS NULL AND status IN ('pending','running');
CREATE INDEX IF NOT EXISTS pluggy_jobs_due ON pluggy_jobs(status,available_at,id);
INSERT INTO schema_migrations(version) VALUES(4) ON CONFLICT DO NOTHING;
