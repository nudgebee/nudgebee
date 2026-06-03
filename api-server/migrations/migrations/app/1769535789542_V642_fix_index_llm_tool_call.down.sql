
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP INDEX idx_llm_conv_agent_conv_id_parent;


-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE INDEX IF NOT EXISTS idx_llm_conv_agent_conv_id
--  ON llm_conversation_agent (conversation_id);


-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE INDEX IF NOT EXISTS idx_llm_conv_agent_conv_id_parent
--   ON llm_conversation_agent (conversation_id)
--   WHERE parent_agent_id IS NULL
--     AND updated_at IS NOT NULL
--     AND created_at IS NOT NULL;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE INDEX IF NOT EXISTS idx_llm_conv_tool_calls_conv_id_tool_type
--   ON llm_conversation_tool_calls (conversation_id, tool_type);
