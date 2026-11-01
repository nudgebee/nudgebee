-- Serves tenant-scoped reads of llm_conversations by time: the llm-server budget check
-- (monthly and daily tenant cost and count, run before every chat and event turn) and the
-- tenant cost dashboards. With no tenant_id index, the tenant-month query scans the whole
-- month through (account_id, created_at) and filters tenant_id afterwards.
--
-- Plain build, run by the migration everywhere: it blocks writes to llm_conversations
-- for as long as the build takes (seconds on ~200k rows). Reads are not blocked.
CREATE INDEX IF NOT EXISTS idx_llm_conversations_tenant_created_at
  ON public.llm_conversations (tenant_id, created_at);
