ALTER TABLE public.llm_conversation_messages 
ADD COLUMN IF NOT EXISTS classification text,
ADD COLUMN IF NOT EXISTS successful_tasks integer DEFAULT 0;
