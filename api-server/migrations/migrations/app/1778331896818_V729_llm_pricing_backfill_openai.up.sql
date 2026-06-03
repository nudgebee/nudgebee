-- V729: Backfill caching rates for OpenAI models. Bedrock Llama 4 deliberately
-- left untouched (does not support prompt caching).
--
-- Without this migration, effectiveCacheCreationRate and effectiveCachedInputRate
-- fall back to the standard input rate (see conversation_dao.go:1925-1939),
-- which over-bills tenants on every cached read by ~50-90% depending on model.
-- Same shape of bug we just fixed in V728 for Gemini + Anthropic.
--
-- Source (verified 2026-05-09):
--   https://developers.openai.com/api/docs/pricing
--
-- OpenAI prompt caching mechanics:
--   - Read discount only — NO separate creation/write fee.
--   - NO storage fee — caches expire automatically (~5-10 min idle, max 1h).
--   - Discount tier varies by model family:
--       gpt-4o family   -> 50% of input
--       o-series        -> 25% of input (o3, o4-mini, o3-mini, o1, o1-mini)
--       gpt-5 family    -> 10% of input
--       gpt-4.1 family  -> 25% of input (not in our DB yet)
--
-- All OpenAI rows therefore use:
--     cost_per_million_cache_creation_tokens   = 0      (no creation fee)
--     cost_per_million_cached_storage_per_hour = NULL   (no storage fee)
--   (no long-context tier columns — OpenAI doesn't have a tier-based input rate)

-- gpt-4o family: cached read = 50% of input
UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens   = 1.25,
    cost_per_million_cache_creation_tokens = 0
WHERE provider_name = 'openai' AND model_name = 'gpt-4o';

UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens   = 0.075,
    cost_per_million_cache_creation_tokens = 0
WHERE provider_name = 'openai' AND model_name = 'gpt-4o-mini';

-- gpt-5 family: cached read = 10% of input
UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens   = 0.125,
    cost_per_million_cache_creation_tokens = 0
WHERE provider_name = 'openai' AND model_name = 'gpt-5';

UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens   = 0.025,
    cost_per_million_cache_creation_tokens = 0
WHERE provider_name = 'openai' AND model_name = 'gpt-5-mini';

UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens   = 0.005,
    cost_per_million_cache_creation_tokens = 0
WHERE provider_name = 'openai' AND model_name = 'gpt-5-nano';

-- o-series reasoning models: cached read = 25% of input
UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens   = 0.50,
    cost_per_million_cache_creation_tokens = 0
WHERE provider_name = 'openai' AND model_name = 'o3';

UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens   = 0.275,
    cost_per_million_cache_creation_tokens = 0
WHERE provider_name = 'openai' AND model_name = 'o4-mini';

-- Bedrock (Llama 4): prompt caching not supported. NULLs left as-is.
-- The fallback path in CalculateTotalCost is moot because cache_creation_tokens
-- and cached_input_tokens are always 0 for Bedrock — there's no caching layer
-- writing those fields. If Bedrock ever adds caching support, a follow-up
-- migration will populate these columns.
