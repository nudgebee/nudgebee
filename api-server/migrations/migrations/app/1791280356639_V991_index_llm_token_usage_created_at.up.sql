-- Index llm_conversation_token_usage by when each call ran. The daily rollup
-- job (V990) rebuilds one day at a time, and the budget fallback query reads a
-- day or month of calls; both filter on created_at alone. Part of #40241.
--
-- A plain build scans the table (~2 GB on dev) and blocks inserts on it for
-- the duration: a few seconds, the same class as V918/V983 on this table. It is
-- its own file so the build does not hold V990's lock. On managed environments
-- pre-apply before merge so this is a no-op:
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_llm_token_usage_created_at
--     ON public.llm_conversation_token_usage (created_at);

SET LOCAL lock_timeout = '5s';

CREATE INDEX IF NOT EXISTS idx_llm_token_usage_created_at
    ON public.llm_conversation_token_usage (created_at);
