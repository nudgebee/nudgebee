-- #42768: the unique key on llm_conversation_tool_calls stored the raw tool_id
-- in its btree entry. Gateway-routed Gemini tool-call IDs carry the thought
-- signature (`<id>_ts_<signature>`) and exceed the ~2.7KB btree row limit, so
-- those inserts failed. Key on md5(tool_id) instead; the full ID stays in the row.
--
-- Pre-apply out-of-band on large databases (makes this statement a no-op):
--   CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS llm_conversation_tool_calls_unique_tool_id_md5
--     ON public.llm_conversation_tool_calls (tool_name, conversation_id, message_id, md5(tool_id), agent_id);
-- Otherwise the build holds a write lock on the table for its duration
-- (seconds at ~500k rows). Must ship with the matching ON CONFLICT target in
-- llm-server's SaveConversationToolCall.
CREATE UNIQUE INDEX IF NOT EXISTS llm_conversation_tool_calls_unique_tool_id_md5
    ON public.llm_conversation_tool_calls (tool_name, conversation_id, message_id, md5(tool_id), agent_id);

ALTER TABLE public.llm_conversation_tool_calls
    DROP CONSTRAINT IF EXISTS llm_conversation_tool_calls_tool_name_conversation_id_message_i;
