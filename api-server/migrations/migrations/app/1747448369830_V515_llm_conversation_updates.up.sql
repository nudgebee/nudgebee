ALTER TABLE public.llm_conversation_agent DROP CONSTRAINT if exists llm_conversation_agent_status_check;
ALTER TABLE public.llm_conversation_agent ADD CONSTRAINT llm_conversation_agent_status_check CHECK ((status = ANY (ARRAY['success'::text, 'fail'::text, 'in_progress'::text, 'waiting'::text, 'skipped'::text])));

alter table public.llm_conversation_agent add column if not exists response_summary varchar(255);
