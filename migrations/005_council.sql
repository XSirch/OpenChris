CREATE TABLE IF NOT EXISTS council_manifests(
 id uuid PRIMARY KEY, source_event_id bigint NOT NULL REFERENCES conversation_events(id),
 snapshot jsonb NOT NULL, fingerprint text NOT NULL,
 policy_version text NOT NULL DEFAULT 'codex-conversation.v1',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE IF NOT EXISTS council_jobs(
 event_id bigint PRIMARY KEY REFERENCES conversation_events(id),
 case_id uuid NOT NULL, agent_id text NOT NULL CHECK(agent_id ~ '^A(0[1-9]|1[0-9]|2[0-3])$'),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','running','complete','failed','failed_unknown','blocked','stale')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 2),
 context_manifest_id uuid REFERENCES council_manifests(id),
 run_id uuid, lease_token uuid, lease_expires_at timestamptz,
 next_run_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 error_code text, completed_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX IF NOT EXISTS council_pending ON council_jobs(event_id) WHERE status IN ('pending','running');
CREATE TABLE IF NOT EXISTS council_attempts(
 run_id uuid PRIMARY KEY, event_id bigint NOT NULL REFERENCES council_jobs(event_id),
 quota_day date NOT NULL, started_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX IF NOT EXISTS council_quota_days ON council_attempts(quota_day);
CREATE TABLE IF NOT EXISTS council_results(
 source_event_id bigint PRIMARY KEY REFERENCES council_jobs(event_id),
 run_id uuid NOT NULL UNIQUE, context_manifest_id uuid NOT NULL REFERENCES council_manifests(id),
 result jsonb NOT NULL, envelope jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO schema_migrations(version) VALUES(5) ON CONFLICT DO NOTHING;
