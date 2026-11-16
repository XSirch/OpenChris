CREATE TABLE IF NOT EXISTS finance_categories(
 id uuid PRIMARY KEY,
 user_id uuid NOT NULL,
 name text NOT NULL CHECK(char_length(btrim(name)) BETWEEN 1 AND 60),
 color text NOT NULL CHECK(color ~ '^#[0-9a-fA-F]{6}$'),
 version integer NOT NULL DEFAULT 1 CHECK(version > 0),
 source text NOT NULL DEFAULT 'manual' CHECK(source = 'manual'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(user_id,id)
);
CREATE UNIQUE INDEX IF NOT EXISTS finance_category_names ON finance_categories(user_id,lower(name));

CREATE TABLE IF NOT EXISTS finance_transactions(
 id uuid PRIMARY KEY,
 user_id uuid NOT NULL,
 transaction_date date NOT NULL,
 description text NOT NULL CHECK(char_length(btrim(description)) BETWEEN 1 AND 200),
 category_id uuid NOT NULL,
 account text NOT NULL CHECK(char_length(btrim(account)) BETWEEN 1 AND 80),
 status text NOT NULL CHECK(status IN ('confirmed','pending')),
 amount_cents bigint NOT NULL CHECK(amount_cents BETWEEN 0 AND 1000000000000),
 recurring boolean NOT NULL DEFAULT false,
 idempotency_key text NOT NULL,
 request_hash text NOT NULL,
 version integer NOT NULL DEFAULT 1 CHECK(version > 0),
 source text NOT NULL DEFAULT 'manual' CHECK(source = 'manual'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(user_id,idempotency_key),
 FOREIGN KEY(user_id,category_id) REFERENCES finance_categories(user_id,id)
);
CREATE INDEX IF NOT EXISTS finance_transaction_dates ON finance_transactions(user_id,transaction_date DESC,created_at DESC);

CREATE TABLE IF NOT EXISTS finance_net_worth(
 id uuid PRIMARY KEY,
 user_id uuid NOT NULL,
 observation_date date NOT NULL,
 assets_cents bigint NOT NULL CHECK(assets_cents BETWEEN 0 AND 1000000000000),
 liabilities_cents bigint NOT NULL CHECK(liabilities_cents BETWEEN 0 AND 1000000000000),
 net_cents bigint GENERATED ALWAYS AS (assets_cents - liabilities_cents) STORED,
 idempotency_key text NOT NULL,
 request_hash text NOT NULL,
 version integer NOT NULL DEFAULT 1 CHECK(version > 0),
 source text NOT NULL DEFAULT 'manual' CHECK(source = 'manual'),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(user_id,idempotency_key)
);
CREATE INDEX IF NOT EXISTS finance_net_worth_dates ON finance_net_worth(user_id,observation_date DESC,updated_at DESC);

CREATE TABLE IF NOT EXISTS finance_audit(
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 user_id uuid NOT NULL,
 entity_type text NOT NULL CHECK(entity_type IN ('category','expense','net_worth')),
 entity_id uuid NOT NULL,
 version integer NOT NULL CHECK(version > 0),
 action text NOT NULL CHECK(action IN ('created','updated')),
 payload jsonb NOT NULL,
 source text NOT NULL DEFAULT 'manual' CHECK(source = 'manual'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(user_id,entity_type,entity_id,version)
);

-- Defaults are configuration only; no financial amounts are seeded.
INSERT INTO finance_categories(id,user_id,name,color) VALUES
 ('10000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000000001','Moradia','#9B6A4D'),
 ('10000000-0000-4000-8000-000000000002','00000000-0000-4000-8000-000000000001','Alimentação','#B98B40'),
 ('10000000-0000-4000-8000-000000000003','00000000-0000-4000-8000-000000000001','Transporte','#527F9A'),
 ('10000000-0000-4000-8000-000000000004','00000000-0000-4000-8000-000000000001','Saúde','#7B9567'),
 ('10000000-0000-4000-8000-000000000005','00000000-0000-4000-8000-000000000001','Lazer','#9B7BA6'),
 ('10000000-0000-4000-8000-000000000006','00000000-0000-4000-8000-000000000001','Outros','#8E9190')
ON CONFLICT DO NOTHING;
INSERT INTO schema_migrations(version) VALUES(2) ON CONFLICT DO NOTHING;
