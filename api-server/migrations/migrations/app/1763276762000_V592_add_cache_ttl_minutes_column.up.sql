-- Add cache_ttl_minutes column to store TTL at insert time for accurate cost calculation
ALTER TABLE public.llm_conversation_token_usage
ADD COLUMN cache_ttl_minutes int4 NULL;

-- Backfill existing entries that have cached tokens with current default TTL (4 minutes)
-- This is the best approximation for historical data
UPDATE public.llm_conversation_token_usage
SET cache_ttl_minutes = 4
WHERE cached_input_tokens > 0 OR cache_creation_tokens > 0;

COMMENT ON COLUMN public.llm_conversation_token_usage.cache_ttl_minutes IS
'Cache TTL in minutes at the time of request. Used for accurate cache cost calculation.';
