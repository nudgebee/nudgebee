ALTER TABLE public.llm_model_pricing
  DROP COLUMN IF EXISTS cost_per_million_cached_input_tokens,
  DROP COLUMN IF EXISTS cost_per_million_cache_creation_tokens,
  DROP COLUMN IF EXISTS cost_per_million_cached_storage_per_hour,
  DROP COLUMN IF EXISTS context_threshold_tokens,
  DROP COLUMN IF EXISTS cost_per_million_input_tokens_long_ctx,
  DROP COLUMN IF EXISTS cost_per_million_output_tokens_long_ctx,
  DROP COLUMN IF EXISTS cost_per_million_cached_input_tokens_long_ctx,
  DROP COLUMN IF EXISTS cost_per_million_cache_creation_tokens_long_ctx;
