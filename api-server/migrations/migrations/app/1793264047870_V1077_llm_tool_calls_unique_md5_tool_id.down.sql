-- Fails if a row with a tool_id too long for a btree entry exists.
ALTER TABLE public.llm_conversation_tool_calls
    ADD CONSTRAINT llm_conversation_tool_calls_tool_name_conversation_id_message_i
    UNIQUE (tool_name, conversation_id, message_id, tool_id, agent_id);

DROP INDEX IF EXISTS public.llm_conversation_tool_calls_unique_tool_id_md5;
