ALTER TABLE public.llm_conversation_tool_calls ADD if not exists "references" text NULL;
ALTER TABLE public.llm_conversation_messages ADD if not exists "suggestions" text NULL;
