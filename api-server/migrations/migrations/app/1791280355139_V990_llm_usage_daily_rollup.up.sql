-- Daily rollup of LLM token usage, so budget checks read a few small rows
-- instead of every llm_conversation_token_usage row in the month
-- (218k scattered rows per tenant-month on dev; 0.3-4.6 s cold). Part of #40241.
--
-- llm_usage_daily holds token sums per (tenant, account, module, day,
-- provider, model, long-context tier). Budgets price them at read time with the
-- same formula as the raw query, so pricing edits still apply. A leader-elected
-- llm-server job rebuilds days from llm_conversation_token_usage (yesterday and
-- today every minute, plus any day not yet built or that failed);
-- llm_usage_rollup_state records which days are built, so budgets fall back to
-- the raw query for a period that is not complete and fresh.
--
-- conversation_account_id records the account of the conversation a call ran
-- for: budgets are enforced per conversation account, which a call's own
-- account_id differs from for cross-account and delegated work. Filled on
-- insert and when the conversation is deleted, like tenant_id/session_id (V986).
--
-- Lock window: ADD COLUMN (nullable, no default) is catalog-only; it takes a
-- brief ACCESS EXCLUSIVE lock on llm_conversation_token_usage, bounded by
-- lock_timeout so the migration fails fast instead of queueing chat traffic.

SET LOCAL lock_timeout = '5s';

ALTER TABLE public.llm_conversation_token_usage
    ADD COLUMN IF NOT EXISTS conversation_account_id uuid;

COMMENT ON COLUMN public.llm_conversation_token_usage.conversation_account_id IS
    'Account of the conversation the call ran for (the account budgets are enforced on), copied so it survives the conversation being deleted. May differ from account_id for cross-account calls.';

CREATE TABLE IF NOT EXISTS public.llm_usage_daily (
    day date NOT NULL,
    tenant_id uuid NOT NULL,
    account_id uuid,
    module text NOT NULL,
    llm_provider text,
    llm_model text,
    long_ctx boolean NOT NULL,
    non_cached_input_tokens bigint NOT NULL,
    cached_input_tokens bigint NOT NULL,
    cache_creation_tokens bigint NOT NULL,
    output_tokens bigint NOT NULL,
    calls integer NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_llm_usage_daily_tenant ON public.llm_usage_daily (tenant_id, module, day);
CREATE INDEX IF NOT EXISTS idx_llm_usage_daily_account ON public.llm_usage_daily (account_id, module, day);
CREATE INDEX IF NOT EXISTS idx_llm_usage_daily_day ON public.llm_usage_daily (day);

CREATE TABLE IF NOT EXISTS public.llm_usage_rollup_state (
    day date PRIMARY KEY,
    status text NOT NULL,
    computed_at timestamptz NOT NULL,
    row_count integer NOT NULL DEFAULT 0,
    error text
);

COMMENT ON TABLE public.llm_usage_daily IS
    'Per-day LLM token sums for budget checks, rebuilt by llm-server from llm_conversation_token_usage. Derived data: safe to truncate together with llm_usage_rollup_state.';
COMMENT ON TABLE public.llm_usage_rollup_state IS
    'Which llm_usage_daily days are built (status ok) and when. Delete a day''s row to have it rebuilt.';
