-- The two cleanup jobs that clear stored prompt and response text from
-- llm_conversation_token_usage look for successful calls older than their
-- retention window that still hold text. Nearly every row past the window has
-- already been cleared, so the created_at index (V991) cannot narrow the
-- search and each run reads the whole table to find the few rows left
-- (measured: 68.7k pages read for 415 rows). This index holds only the rows
-- that still have text to clear, so a run reads just those. Its predicate is
-- the jobs' own WHERE clause; keep the two in step. Part of #38283.
--
-- A plain build scans the table and blocks inserts on it for the duration: a
-- few seconds, the same class as V991/V992 on this table. On managed
-- environments pre-apply before merge so this is a no-op:
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_llm_token_usage_pending_cleanup
--     ON public.llm_conversation_token_usage (created_at)
--     WHERE request_status = 'success'
--       AND (prompt_messages IS NOT NULL OR response_content IS NOT NULL);

SET LOCAL lock_timeout = '5s';

CREATE INDEX IF NOT EXISTS idx_llm_token_usage_pending_cleanup
    ON public.llm_conversation_token_usage (created_at)
    WHERE request_status = 'success'
      AND (prompt_messages IS NOT NULL OR response_content IS NOT NULL);
