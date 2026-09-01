-- Dedup key for NULL user_id conversations (GH #37367): Postgres treats
-- NULL <> NULL, so the existing UNIQUE (session_id, user_id, account_id)
-- constraint never catches a second concurrent save for the same session_id.
--
-- No CONCURRENTLY (Atlas Community runs migrations in a transaction). Pre-apply
-- out-of-band on each live DB before merging:
--   CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS llm_conversations_session_account_null_user_uniq
--     ON public.llm_conversations (session_id, account_id) WHERE user_id IS NULL;
-- Skipping that means this statement takes an ACCESS EXCLUSIVE lock instead.
CREATE UNIQUE INDEX IF NOT EXISTS llm_conversations_session_account_null_user_uniq
  ON public.llm_conversations (session_id, account_id)
  WHERE user_id IS NULL;
