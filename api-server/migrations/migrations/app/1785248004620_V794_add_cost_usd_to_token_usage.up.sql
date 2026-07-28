ALTER TABLE public.llm_conversation_token_usage
ADD COLUMN cost_usd float8 NULL;

COMMENT ON COLUMN public.llm_conversation_token_usage.cost_usd IS
'USD cost of this call, computed from llm_model_pricing at insert time. NULL for legacy rows and calls with no matching pricing entry — readers fall back to a live pricing JOIN in that case.';
