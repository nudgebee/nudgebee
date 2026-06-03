-- Composite indexes for ai_get_conversation_v3 delta-fetch.
-- The new endpoint runs 3 small queries that filter by conversation/message/agent
-- AND a recent updated_at cursor. The existing single-column FK indexes from V646
-- (conversation_id / message_id / agent_id) already make initial loads fast, but
-- polling deltas need the trailing updated_at column for an index-only range scan.
CREATE INDEX IF NOT EXISTS idx_llm_conv_messages_conv_updated
  ON public.llm_conversation_messages (conversation_id, updated_at)
  WHERE message_type IN ('generation', 'followup');

CREATE INDEX IF NOT EXISTS idx_llm_conv_agent_message_updated
  ON public.llm_conversation_agent (message_id, updated_at);

CREATE INDEX IF NOT EXISTS idx_llm_conv_tool_calls_agent_updated
  ON public.llm_conversation_tool_calls (agent_id, updated_at);
