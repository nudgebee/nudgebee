ALTER TABLE public.llm_conversation_tool_calls DROP constraint if exists llm_conversation_tool_calls_status_check;
ALTER TABLE public.llm_conversation_tool_calls ADD CONSTRAINT llm_conversation_tool_calls_status_check CHECK ((status = ANY (ARRAY['success'::text, 'fail'::text, 'in_progress'::text, 'error'::text, 'waiting'::text])));

ALTER TABLE public.llm_conversation_agent DROP CONSTRAINT  if exists  llm_conversation_agent_status_check;
ALTER TABLE public.llm_conversation_agent ADD CONSTRAINT llm_conversation_agent_status_check CHECK ((status = ANY (ARRAY['success'::text, 'fail'::text, 'in_progress'::text, 'waiting'::text])));


alter table llm_conversation_agent add column if not exists state text;

alter table llm_conversation_tool_calls add column if not exists tool_type text;

alter table llm_conversation_tool_calls add column if not exists child_agent_id text;

update llm_conversation_tool_calls set tool_type = 'tool' where tool_type is null;