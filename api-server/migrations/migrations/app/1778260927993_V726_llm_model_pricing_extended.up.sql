-- Extended LLM pricing schema to support correct cache cost calculation across
-- providers + Gemini's two-tier pricing (>200K context).
--
-- Why each column:
--
--   cost_per_million_cached_input_tokens
--     Provider-specific *cached read* rate. Today the formula misuses
--     cache_cost_per_million_tokens_per_hour as both "storage" and "cached
--     read", which is wrong. With this column, cached reads are billed
--     correctly via tokens × rate.
--
--   cost_per_million_cache_creation_tokens
--     Provider-specific *cache write* rate. Anthropic charges 1.25x input rate
--     for cache writes; Vertex/Gemini charges 1x. NULL in this column means
--     "use cost_per_million_input_tokens" (Vertex/Gemini default).
--
--   cost_per_million_cached_storage_per_hour
--     Per-token-hour storage rate, billed per-cache-lifetime (NOT per-call).
--     Only Vertex/Gemini bills storage explicitly; NULL elsewhere → storage
--     cost = 0 for Anthropic / OpenAI / Bedrock (correct).
--
--   context_threshold_tokens + *_long_ctx columns
--     Two-tier pricing: Gemini Pro/Flash bill 2x rates when total prompt
--     tokens > 200000. NULL threshold → no tiering (Anthropic, OpenAI etc.).
--
-- Existing column cache_cost_per_million_tokens_per_hour is left in place; a
-- follow-up migration drops it after backfill into the new storage column.

ALTER TABLE public.llm_model_pricing
  ADD COLUMN cost_per_million_cached_input_tokens             real NULL,
  ADD COLUMN cost_per_million_cache_creation_tokens           real NULL,
  ADD COLUMN cost_per_million_cached_storage_per_hour         real NULL,
  ADD COLUMN context_threshold_tokens                         int4 NULL,
  ADD COLUMN cost_per_million_input_tokens_long_ctx           real NULL,
  ADD COLUMN cost_per_million_output_tokens_long_ctx          real NULL,
  ADD COLUMN cost_per_million_cached_input_tokens_long_ctx    real NULL,
  ADD COLUMN cost_per_million_cache_creation_tokens_long_ctx  real NULL;

COMMENT ON COLUMN public.llm_model_pricing.cost_per_million_cached_input_tokens IS
  'USD/M tokens for reads served from prompt cache. NULL = use input rate.';
COMMENT ON COLUMN public.llm_model_pricing.cost_per_million_cache_creation_tokens IS
  'USD/M tokens for writing tokens into the prompt cache. NULL = use input rate (Vertex/Gemini).';
COMMENT ON COLUMN public.llm_model_pricing.cost_per_million_cached_storage_per_hour IS
  'USD/M tokens × hour while the cache exists. NULL = provider does not bill storage (Anthropic/OpenAI/Bedrock).';
COMMENT ON COLUMN public.llm_model_pricing.context_threshold_tokens IS
  'Prompt-token threshold above which long-context rates apply (e.g. 200000 for Gemini Pro). NULL = no tiering.';
COMMENT ON COLUMN public.llm_model_pricing.cost_per_million_input_tokens_long_ctx IS
  'Input rate when total prompt > context_threshold_tokens. NULL = no tiering.';
COMMENT ON COLUMN public.llm_model_pricing.cost_per_million_output_tokens_long_ctx IS
  'Output rate when total prompt > context_threshold_tokens. NULL = no tiering.';
COMMENT ON COLUMN public.llm_model_pricing.cost_per_million_cached_input_tokens_long_ctx IS
  'Cached-read rate when total prompt > context_threshold_tokens. NULL = no tiering.';
COMMENT ON COLUMN public.llm_model_pricing.cost_per_million_cache_creation_tokens_long_ctx IS
  'Cache-write rate when total prompt > context_threshold_tokens. NULL = no tiering.';
