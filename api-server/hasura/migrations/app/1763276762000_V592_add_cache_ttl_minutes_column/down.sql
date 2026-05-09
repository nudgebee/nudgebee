-- Remove cache_ttl_minutes column
ALTER TABLE public.llm_conversation_token_usage
DROP COLUMN IF EXISTS cache_ttl_minutes;
