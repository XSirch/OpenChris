-- Somente em banco de teste descartável, após backup; não executado automaticamente.
DROP TABLE IF EXISTS context_manifests,memory_facts,episodes,jobs,conversation_threads,conversation_events,stream_events,sessions,benchmark_events,schema_migrations CASCADE;
