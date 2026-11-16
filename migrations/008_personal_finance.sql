CREATE TABLE IF NOT EXISTS finance_profile (
 user_id uuid PRIMARY KEY,
 version integer NOT NULL CHECK(version>0),
 data jsonb NOT NULL,
 confirmed_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE IF NOT EXISTS finance_classifications (
 user_id uuid NOT NULL,
 transaction_id text NOT NULL,
 category text NOT NULL CHECK(char_length(btrim(category)) BETWEEN 1 AND 60),
 kind text NOT NULL CHECK(kind IN ('expense','income','transfer_internal','investment','card_bill_payment','refund','unknown')),
 version integer NOT NULL CHECK(version>0),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(user_id,transaction_id),
 FOREIGN KEY(user_id,transaction_id) REFERENCES pluggy_transactions(user_id,external_id)
);

CREATE TABLE IF NOT EXISTS finance_classification_rules (
 user_id uuid NOT NULL,
 match_key text NOT NULL CHECK(char_length(match_key) BETWEEN 1 AND 240),
 category text NOT NULL CHECK(char_length(btrim(category)) BETWEEN 1 AND 60),
 kind text NOT NULL CHECK(kind IN ('expense','income','transfer_internal','investment','card_bill_payment','refund','unknown')),
 version integer NOT NULL DEFAULT 1 CHECK(version>0),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(user_id,match_key)
);

CREATE TABLE IF NOT EXISTS finance_personal_audit (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 user_id uuid NOT NULL,
 entity text NOT NULL CHECK(entity IN ('profile','classification')),
 entity_id text NOT NULL,
 version integer NOT NULL,
 payload jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO schema_migrations(version) VALUES(8) ON CONFLICT DO NOTHING;
