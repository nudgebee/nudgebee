-- Index llm_conversation_token_usage by the account a call's spend belongs to
-- and when the call ran, so the AI Cost Analyser and the cost report can read
-- an account's calls in a window directly. Filtering through
-- llm_conversations instead has to probe every conversation the account ever
-- had (2.3 s for one account-month on dev). Part of #40241.
--
-- conversation_account_id (V990) is empty on rows written before V990; the
-- llm-server usage-account backfill job fills them in batches after deploy.
--
-- A plain build scans the table (~2 GB on dev, ~600 MB on prod) and blocks
-- inserts on it for the duration: a few seconds, the same class as V991. It is
-- its own file so it does not hold V993's lock.

SET LOCAL lock_timeout = '5s';

CREATE INDEX IF NOT EXISTS idx_llm_token_usage_conv_account_created_at
    ON public.llm_conversation_token_usage (conversation_account_id, created_at);
