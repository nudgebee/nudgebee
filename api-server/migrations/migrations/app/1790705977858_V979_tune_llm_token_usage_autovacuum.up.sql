-- llm_conversation_token_usage gets one insert per LLM call, and the usage cleanup cron
-- (api-server/services/nb/llm_token_usage_cleanup.go) rewrites every row hours later to
-- null its prompt/response bodies. With the default autovacuum thresholds (20% of the
-- table dead, i.e. ~330k rows at 1.66M), autovacuum almost never runs: on dev it last ran
-- a month ago, leaving ~118k dead rows and only 27% of pages all-visible. The llm-server
-- budget check reads ~124k of these rows per tenant-month query, and that fetch is ~600 ms
-- of its ~700 ms.
--
-- Lowering the per-table thresholds lets Postgres's own autovacuum keep the table clean on
-- every install. No manual VACUUM is needed; the first run starts on its own once this
-- applies. ALTER TABLE ... SET takes a SHARE UPDATE EXCLUSIVE lock: reads and writes
-- continue, and it completes immediately.
ALTER TABLE public.llm_conversation_token_usage SET (
  autovacuum_vacuum_scale_factor = 0.02,
  autovacuum_analyze_scale_factor = 0.02
);

-- Insert-driven vacuum (which also marks newly inserted pages all-visible) exists only on
-- PostgreSQL 13+. The bundled chart ships 16, but external databases may be older.
DO $$
BEGIN
  IF current_setting('server_version_num')::int >= 130000 THEN
    EXECUTE 'ALTER TABLE public.llm_conversation_token_usage SET (autovacuum_vacuum_insert_scale_factor = 0.02)';
  END IF;
END $$;
