-- #39745: deleting a conversation took 2.5 s on average and up to 32 s. Removing a
-- row from llm_conversation_messages makes Postgres check (or cascade into) every
-- table that references messages(id), and three of those foreign-key columns had
-- no index, so each check scanned the whole child table once per deleted message.
-- Measured on dev, per deleted message:
--   llm_conversation_token_usage.message_id       68,699 pages (~2 s cold) -> 6 pages
--   llm_conversation_agent_critiques.message_id   110 ms scan              -> 2 pages
--   llm_conversation_agent_critiques.conversation_id (cascade from llm_conversations)
--
-- Pre-apply out of band on each managed env before this reaches it (migrations-lint
-- rejects CONCURRENTLY here), then confirm each index is valid -- a failed concurrent
-- build leaves an INVALID index that IF NOT EXISTS below would silently keep:
--   SET lock_timeout = '120s'; SET statement_timeout = 0;
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_llm_conversation_token_usage_message_id
--     ON llm_conversation_token_usage (message_id);
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_llm_conv_agent_critiques_message_id
--     ON llm_conversation_agent_critiques (message_id);
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_llm_conv_agent_critiques_conversation_id
--     ON llm_conversation_agent_critiques (conversation_id);
--   SELECT indexrelid::regclass, indisvalid FROM pg_index
--    WHERE indexrelid::regclass::text LIKE 'idx_llm_conv%message_id'
--       OR indexrelid::regclass::text = 'idx_llm_conv_agent_critiques_conversation_id';
-- Where pre-built, the statements below are no-ops. On on-prem they are plain builds
-- that block writes to each table while they run: 2.2 s concurrently on dev for the
-- 1.6M-row token-usage table (19 MB index), well under a second for critiques.
--
-- llm_conversation_tool_calls.message_id is left without its own index on purpose:
-- its check costs ~90 ms per message through an existing composite index, and the
-- table is the largest here with no HOT updates, so a sixth index would tax every
-- write to save ~0.1 s on a typical 1-2 message delete.

CREATE INDEX IF NOT EXISTS idx_llm_conversation_token_usage_message_id
  ON llm_conversation_token_usage (message_id);

CREATE INDEX IF NOT EXISTS idx_llm_conv_agent_critiques_message_id
  ON llm_conversation_agent_critiques (message_id);

CREATE INDEX IF NOT EXISTS idx_llm_conv_agent_critiques_conversation_id
  ON llm_conversation_agent_critiques (conversation_id);
