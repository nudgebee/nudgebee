-- Fix slow fetchConversation: add missing indexes on FK join columns
-- The 3-level nested GraphQL query (conversations → messages → agents → tool_calls)
-- was doing sequential scans on 162K-876K row tables due to missing indexes.
-- EXPLAIN ANALYZE showed: messages 82ms, agents 137ms, tool_calls 1120ms per lookup.

-- FK join columns for the nested GraphQL query (highest impact)
CREATE INDEX IF NOT EXISTS idx_llm_conv_messages_conversation_id
  ON llm_conversation_messages(conversation_id);

CREATE INDEX IF NOT EXISTS idx_llm_conv_agent_message_id
  ON llm_conversation_agent(message_id);

CREATE INDEX IF NOT EXISTS idx_llm_conv_tool_calls_agent_id
  ON llm_conversation_tool_calls(agent_id);

-- WHERE clause and permission filter columns
CREATE INDEX IF NOT EXISTS idx_llm_conversations_session_id
  ON llm_conversations(session_id);

CREATE INDEX IF NOT EXISTS idx_llm_conversations_account_id
  ON llm_conversations(account_id);

CREATE INDEX IF NOT EXISTS idx_llm_conv_messages_account_id
  ON llm_conversation_messages(account_id);

-- Refresh stale statistics (pg_stat showed 259 rows when actual is 82K)
ANALYZE llm_conversations;
ANALYZE llm_conversation_messages;
ANALYZE llm_conversation_agent;
ANALYZE llm_conversation_tool_calls;
