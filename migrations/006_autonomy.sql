CREATE TABLE IF NOT EXISTS autonomy_state(
 user_id uuid PRIMARY KEY, paused boolean NOT NULL DEFAULT false,
 heartbeat_at timestamptz, next_collect_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 next_run_at timestamptz NOT NULL DEFAULT clock_timestamp(), last_run_at timestamptz,
 material_fingerprint text NOT NULL DEFAULT '', requested boolean NOT NULL DEFAULT false,
 collect_token uuid, collect_expires_at timestamptz, notice_epoch timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE IF NOT EXISTS autonomy_sources(
 id text PRIMARY KEY, name text NOT NULL, url text NOT NULL UNIQUE, kind text NOT NULL,
 enabled boolean NOT NULL DEFAULT true, is_feed boolean NOT NULL DEFAULT true,
 state text NOT NULL DEFAULT 'unknown', error_code text NOT NULL DEFAULT '',
 etag text NOT NULL DEFAULT '', last_modified text NOT NULL DEFAULT '',
 last_checked_at timestamptz, last_success_at timestamptz
);
CREATE TABLE IF NOT EXISTS autonomy_evidence(
 id uuid PRIMARY KEY, source_id text NOT NULL REFERENCES autonomy_sources(id),
 url text NOT NULL, title text NOT NULL, summary text NOT NULL, kind text NOT NULL,
 content_hash text NOT NULL, published_at timestamptz, observed_at timestamptz NOT NULL,
 UNIQUE(url,content_hash)
);
CREATE INDEX IF NOT EXISTS autonomy_evidence_observed ON autonomy_evidence(observed_at DESC);
CREATE TABLE IF NOT EXISTS autonomy_evidence_observations(
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 evidence_id uuid NOT NULL REFERENCES autonomy_evidence(id),
 source_id text NOT NULL REFERENCES autonomy_sources(id), url text NOT NULL,
 observed_at timestamptz NOT NULL
);
DO $$ DECLARE receipt_constraint text; BEGIN
 FOR receipt_constraint IN SELECT conname FROM pg_constraint
  WHERE conrelid='autonomy_evidence_observations'::regclass AND contype='u'
   AND conkey=ARRAY[3,2,5]::smallint[]
 LOOP EXECUTE format('ALTER TABLE autonomy_evidence_observations DROP CONSTRAINT %I',receipt_constraint); END LOOP;
END $$;
CREATE INDEX IF NOT EXISTS autonomy_observation_latest ON autonomy_evidence_observations(url,observed_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS autonomy_observation_source ON autonomy_evidence_observations(source_id,url,observed_at DESC,id DESC);
INSERT INTO autonomy_evidence_observations(evidence_id,source_id,url,observed_at)
 SELECT e.id,e.source_id,e.url,e.observed_at FROM autonomy_evidence e
 WHERE NOT EXISTS(SELECT 1 FROM autonomy_evidence_observations o WHERE o.evidence_id=e.id AND o.source_id=e.source_id AND o.observed_at=e.observed_at)
 ORDER BY e.observed_at,e.id;
CREATE TABLE IF NOT EXISTS autonomy_runs(
 id uuid PRIMARY KEY, user_id uuid NOT NULL, status text NOT NULL DEFAULT 'pending'
 CHECK(status IN ('pending','running','complete','await_data','partial','stale')),
 reason text NOT NULL, topic text NOT NULL, fingerprint text NOT NULL,
 evidence_fingerprint text NOT NULL DEFAULT '', sources_healthy boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL, completed_at timestamptz, error_code text NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS autonomy_one_open_run ON autonomy_runs(user_id) WHERE status IN ('pending','running');
CREATE TABLE IF NOT EXISTS autonomy_run_evidence(
 run_id uuid NOT NULL REFERENCES autonomy_runs(id), evidence_id uuid NOT NULL REFERENCES autonomy_evidence(id),
 PRIMARY KEY(run_id,evidence_id)
);
ALTER TABLE autonomy_run_evidence ADD COLUMN IF NOT EXISTS observed_at timestamptz;
ALTER TABLE autonomy_run_evidence ADD COLUMN IF NOT EXISTS observation_id bigint REFERENCES autonomy_evidence_observations(id);
UPDATE autonomy_run_evidence r SET observed_at=e.observed_at,
 observation_id=(SELECT max(o.id) FROM autonomy_evidence_observations o WHERE o.evidence_id=e.id AND o.observed_at=e.observed_at)
 FROM autonomy_evidence e WHERE r.evidence_id=e.id AND r.observation_id IS NULL;
CREATE TABLE IF NOT EXISTS autonomy_tasks(
 id uuid PRIMARY KEY, run_id uuid NOT NULL REFERENCES autonomy_runs(id), stage integer NOT NULL CHECK(stage BETWEEN 0 AND 4),
 agent_id text NOT NULL, status text NOT NULL DEFAULT 'pending'
 CHECK(status IN ('pending','running','complete','failed','failed_unknown','stale','cancelled')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 2),
 attempt_id uuid, lease_token uuid, lease_expires_at timestamptz,
 next_run_at timestamptz NOT NULL DEFAULT clock_timestamp(), started_at timestamptz, completed_at timestamptz,
 context jsonb, fingerprint text, result jsonb, error_code text NOT NULL DEFAULT '',
 UNIQUE(run_id,stage)
);
CREATE INDEX IF NOT EXISTS autonomy_pending ON autonomy_tasks(run_id,stage) WHERE status IN ('pending','running');
ALTER TABLE council_attempts ALTER COLUMN event_id DROP NOT NULL;
ALTER TABLE council_attempts ADD COLUMN IF NOT EXISTS origin text NOT NULL DEFAULT 'conversation' CHECK(origin IN ('conversation','autonomy'));
ALTER TABLE council_attempts ADD COLUMN IF NOT EXISTS task_id uuid REFERENCES autonomy_tasks(id);
CREATE INDEX IF NOT EXISTS autonomy_quota_days ON council_attempts(quota_day,origin);
CREATE TABLE IF NOT EXISTS autonomy_strategies(
 id uuid PRIMARY KEY, user_id uuid NOT NULL, identity_key text NOT NULL,
 current_version integer NOT NULL DEFAULT 1, created_at timestamptz NOT NULL,
 UNIQUE(user_id,identity_key)
);
CREATE TABLE IF NOT EXISTS autonomy_strategy_versions(
 strategy_id uuid NOT NULL REFERENCES autonomy_strategies(id), version integer NOT NULL,
 run_id uuid NOT NULL REFERENCES autonomy_runs(id), artifact jsonb NOT NULL, critique jsonb NOT NULL,
 created_at timestamptz NOT NULL, PRIMARY KEY(strategy_id,version), UNIQUE(run_id,strategy_id)
);
CREATE TABLE IF NOT EXISTS autonomy_notices(
 id uuid PRIMARY KEY, user_id uuid NOT NULL, run_id uuid REFERENCES autonomy_runs(id),
 kind text NOT NULL CHECK(kind IN ('summary','alert')), slot_key text NOT NULL,
 delivery_state text NOT NULL DEFAULT 'pending' CHECK(delivery_state IN ('pending','queued','sent','failed','failed_unknown','cancelled')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), UNIQUE(user_id,kind,slot_key)
);
INSERT INTO schema_migrations(version) VALUES(6) ON CONFLICT DO NOTHING;
