ALTER TYPE llm_conversation_status ADD VALUE IF NOT EXISTS 'WAITING';

ALTER TABLE public.llm_conversation_agent DROP CONSTRAINT llm_conversation_agent_status_check;

ALTER TABLE public.llm_conversation_agent ADD CONSTRAINT llm_conversation_agent_status_check CHECK ((status = ANY (ARRAY['success'::text, 'fail'::text, 'in_progress'::text, 'waiting'::text])));

ALTER TABLE public.llm_conversation_agent add column if not exists followup_message_id uuid;