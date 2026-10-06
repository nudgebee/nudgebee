-- Rows whose conversation_account_id the llm-server usage-account backfill job
-- still has to fill: rows written before V990, or by a pod still running older
-- code during a rollout. The job reads the newest of them every minute, so
-- this index keeps that read cheap once the backfill is done and the index is
-- near empty. Part of #40241.
--
-- Same build cost and lock as V992 (a few seconds of blocked inserts).

SET LOCAL lock_timeout = '5s';

CREATE INDEX IF NOT EXISTS idx_llm_token_usage_account_pending
    ON public.llm_conversation_token_usage (created_at)
    WHERE conversation_account_id IS NULL
      AND (conversation_id IS NOT NULL OR tenant_id IS NOT NULL);
