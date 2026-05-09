ALTER TABLE public.llm_conversations ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE public.llm_conversation_messages ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE public.llm_conversation_agent  ALTER COLUMN user_id DROP NOT NULL;

insert into event_source (value) values ('anomaly') on conflict  do nothing;
