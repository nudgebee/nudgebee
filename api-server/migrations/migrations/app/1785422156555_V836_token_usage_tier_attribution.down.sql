ALTER TABLE public.llm_conversation_token_usage
DROP COLUMN IF EXISTS model_tier,
DROP COLUMN IF EXISTS task_type;
