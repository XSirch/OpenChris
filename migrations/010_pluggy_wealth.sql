ALTER TABLE pluggy_items ADD COLUMN IF NOT EXISTS wealth_loans jsonb;
INSERT INTO schema_migrations(version) VALUES(10) ON CONFLICT DO NOTHING;
