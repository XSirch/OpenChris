CREATE TABLE IF NOT EXISTS schema_migrations(version integer PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS conversation_events(
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 user_id uuid NOT NULL, channel text NOT NULL CHECK(channel IN ('telegram','dashboard')),
 external_key text NOT NULL, direction text NOT NULL CHECK(direction IN ('incoming','outgoing')),
 body text NOT NULL, classification text NOT NULL DEFAULT 'restricted',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(user_id,channel,external_key,direction)
);
CREATE TABLE IF NOT EXISTS jobs(
 event_id bigint PRIMARY KEY REFERENCES conversation_events(id),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','complete','failed')),
 attempts integer NOT NULL DEFAULT 0, next_run_at timestamptz NOT NULL DEFAULT now(),
 error_code text, completed_at timestamptz
);
CREATE TABLE IF NOT EXISTS conversation_threads(
 user_id uuid NOT NULL,channel text NOT NULL,
 last_input_event_id bigint NOT NULL REFERENCES conversation_events(id),
 last_output_event_id bigint REFERENCES conversation_events(id),
 state text NOT NULL CHECK(state IN ('pending','complete')),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),PRIMARY KEY(user_id,channel)
);
CREATE TABLE IF NOT EXISTS episodes(
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 user_id uuid NOT NULL, source_event_id bigint NOT NULL UNIQUE REFERENCES conversation_events(id),
 channel text NOT NULL, body text NOT NULL, outcome text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX IF NOT EXISTS episode_search ON episodes USING gin(to_tsvector('portuguese',body));
CREATE TABLE IF NOT EXISTS memory_facts(
 id uuid PRIMARY KEY, user_id uuid NOT NULL, predicate text NOT NULL,
 value text NOT NULL, version integer NOT NULL CHECK(version>0),
 status text NOT NULL CHECK(status IN ('active','superseded')),
 authority text NOT NULL DEFAULT 'USER_EXPLICIT', source_event_id bigint NOT NULL REFERENCES conversation_events(id),
 channel text NOT NULL, supersedes uuid REFERENCES memory_facts(id),
 valid_from timestamptz NOT NULL DEFAULT clock_timestamp(), valid_to timestamptz,
 UNIQUE(user_id,predicate,version)
);
CREATE UNIQUE INDEX IF NOT EXISTS one_active_fact ON memory_facts(user_id,predicate) WHERE status='active';
CREATE TABLE IF NOT EXISTS context_manifests(
 id uuid PRIMARY KEY, user_id uuid NOT NULL, source_event_id bigint NOT NULL UNIQUE REFERENCES conversation_events(id),
 facts jsonb NOT NULL, policy_version text NOT NULL DEFAULT 'synthetic-f1.v1',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE IF NOT EXISTS stream_events(
 sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 user_id uuid NOT NULL, event_type text NOT NULL, payload jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE IF NOT EXISTS sessions(
 token_hash text PRIMARY KEY, csrf text NOT NULL,
 expires_at timestamptz NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS benchmark_events(
 backend text NOT NULL,event_id text NOT NULL,payload text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(backend,event_id)
);
INSERT INTO schema_migrations(version) VALUES(1) ON CONFLICT DO NOTHING;
