-- Catalog top-up for the AI Gateway: the current Gemini "3.x" models that were
-- missing a price row, from Google's published API pricing (Standard tier). Both are
-- served today and are the default targets of the nb-fast / nb-cheap tier aliases, so
-- without these rows the gateway's cost snapshot records $0 for that traffic.
--
-- Rates per 1M tokens: input, output, cached-input (read). Gemini has no separate
-- cache-CREATION charge, so cache-creation is 0 (matching the existing gemini rows).
--   * gemini-3.6-flash      — $1.50 / $7.50 / $0.15 (output cut from 3.5-flash's $9)
--   * gemini-3.5-flash-lite — $0.30 / $2.50 / $0.03
-- Every other current Gemini 3.x model with published pricing is already in the
-- catalog (gemini-3.5-flash, gemini-3.1-flash-lite, gemini-3.1-pro-preview, …); the
-- 3.5/3.6 "pro" variants are not GA yet, so there is nothing to add for them.
-- Idempotent upsert; existing rows are untouched.
INSERT INTO llm_model_pricing (model_name, provider_name,
    cost_per_million_input_tokens, cost_per_million_output_tokens,
    cost_per_million_cached_input_tokens, cost_per_million_cache_creation_tokens) VALUES
    ('gemini-3.6-flash',      'googleai', 1.50, 7.50, 0.15, 0),
    ('gemini-3.5-flash-lite', 'googleai', 0.30, 2.50, 0.03, 0)
ON CONFLICT (model_name, provider_name)
DO UPDATE SET
    cost_per_million_input_tokens = EXCLUDED.cost_per_million_input_tokens,
    cost_per_million_output_tokens = EXCLUDED.cost_per_million_output_tokens,
    cost_per_million_cached_input_tokens = EXCLUDED.cost_per_million_cached_input_tokens,
    cost_per_million_cache_creation_tokens = EXCLUDED.cost_per_million_cache_creation_tokens;
