ALTER TABLE public.llm_conversation_messages 
DROP COLUMN IF EXISTS classification,
DROP COLUMN IF EXISTS successful_tasks;
