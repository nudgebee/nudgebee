alter table llm_conversation_agent add column if not exists agent_step_response text;

ALTER TYPE public."llm_conversation_status" ADD VALUE IF NOT EXISTS 'WAITING_FOR_CLIENT_TOOL';

alter table public.llm_conversation_agent drop constraint if exists llm_conversation_agent_status_check;

ALTER TABLE public.llm_conversation_agent ADD CONSTRAINT llm_conversation_agent_status_check CHECK ((status = ANY (ARRAY['success'::text, 'fail'::text, 'in_progress'::text, 'waiting'::text, 'skipped'::text, 'needs_user_input'::text, 'waiting_for_client_tool'::text])));

alter table public.llm_conversation_tool_calls drop constraint if exists llm_conversation_tool_calls_status_check;

ALTER TABLE public.llm_conversation_tool_calls ADD CONSTRAINT llm_conversation_tool_calls_status_check CHECK ((status = ANY (ARRAY['success'::text, 'fail'::text, 'in_progress'::text, 'error'::text, 'waiting'::text, 'waiting_for_client'::text])));