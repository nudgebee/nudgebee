-- ai_aggregate_tool_usage (Cost Analyser "Tools" tab) is slow and slowing further:
-- core.ListToolUsage's downstream-cost scan joins llm_conversation_token_usage on
-- t.agent_id, which has no index, so it full-scans the table (cost grows with row
-- count, not the query window). A btree on agent_id makes it an index probe.
--
-- Pre-apply out of band on each managed env before merge (migrations-lint rejects
-- CONCURRENTLY here); the statement below is then a no-op, and on on-prem it is a
-- plain locked build:
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_llm_conversation_token_usage_agent_id
--     ON llm_conversation_token_usage (agent_id);

CREATE INDEX IF NOT EXISTS idx_llm_conversation_token_usage_agent_id
  ON llm_conversation_token_usage (agent_id);

ANALYZE llm_conversation_token_usage;
