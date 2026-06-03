-- V728: Backfill caching rates and long-context tier pricing for Gemini + Anthropic.
--
-- The V726 migration added the columns (cost_per_million_cached_input_tokens,
-- cost_per_million_cache_creation_tokens, cost_per_million_cached_storage_per_hour,
-- *_long_ctx variants, context_threshold_tokens) but did NOT populate them.
-- effectiveCacheCreationRate falls back to the input rate when NULL, which
-- silently double-bills cached prefixes (cached_read + creation both at full rate)
-- and reports negative cache savings in /v1/completions/conversation-usage-metrics.
--
-- Sources for these rates (verified 2026-05-09):
--   Gemini:    https://ai.google.dev/gemini-api/docs/pricing
--   Anthropic: https://platform.claude.com/docs/en/docs/build-with-claude/prompt-caching
--
-- Provider rate semantics:
--   Gemini    -> cached read = 10% of input. Creation has NO token fee
--                (free); cache cost is per-hour storage. Long-ctx tier
--                applies to Pro models above the threshold.
--   Anthropic -> cached read = 10% of input. Cache creation = 1.25x input
--                (5-minute ephemeral). NO storage fee.
--
-- OpenAI and Bedrock are intentionally left alone - rate semantics vary
-- across the gpt-4o vs gpt-5/o3/o4 families and need a separate migration.

-- ============================================================================
-- GEMINI
-- ============================================================================

-- gemini-3.1-pro-preview: input $2 / output $12 (<=200K), cached $0.20,
-- storage $4.50/M/hr. Long-ctx (>200K): input $4, output $18, cached $0.40.
UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens            = 0.20,
    cost_per_million_cache_creation_tokens          = 0,
    cost_per_million_cached_storage_per_hour        = 4.50,
    context_threshold_tokens                        = 200000,
    cost_per_million_input_tokens_long_ctx          = 4.00,
    cost_per_million_output_tokens_long_ctx         = 18.00,
    cost_per_million_cached_input_tokens_long_ctx   = 0.40,
    cost_per_million_cache_creation_tokens_long_ctx = 0
WHERE provider_name = 'googleai' AND model_name = 'gemini-3.1-pro-preview';

-- gemini-3-pro-preview: same Pro-tier rates as 3.1 (both listed at $2/$12 input/output).
UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens            = 0.20,
    cost_per_million_cache_creation_tokens          = 0,
    cost_per_million_cached_storage_per_hour        = 4.50,
    context_threshold_tokens                        = 200000,
    cost_per_million_input_tokens_long_ctx          = 4.00,
    cost_per_million_output_tokens_long_ctx         = 18.00,
    cost_per_million_cached_input_tokens_long_ctx   = 0.40,
    cost_per_million_cache_creation_tokens_long_ctx = 0
WHERE provider_name = 'googleai' AND model_name = 'gemini-3-pro-preview';

-- gemini-3-flash-preview: input $0.50 / output $3.00, cached $0.05, storage $1/M/hr.
UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens     = 0.05,
    cost_per_million_cache_creation_tokens   = 0,
    cost_per_million_cached_storage_per_hour = 1.00
WHERE provider_name = 'googleai' AND model_name = 'gemini-3-flash-preview';

-- gemini-3.1-flash-lite-preview: input $0.25 / output $1.50, cached $0.025, storage $1/M/hr.
UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens     = 0.025,
    cost_per_million_cache_creation_tokens   = 0,
    cost_per_million_cached_storage_per_hour = 1.00
WHERE provider_name = 'googleai' AND model_name = 'gemini-3.1-flash-lite-preview';

-- gemini-2.5-pro: input $1.25 / output $10 (<=200K), cached $0.125, storage $4.50/M/hr.
-- Long-ctx (>200K): input $2.50, output $15, cached $0.25.
UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens            = 0.125,
    cost_per_million_cache_creation_tokens          = 0,
    cost_per_million_cached_storage_per_hour        = 4.50,
    context_threshold_tokens                        = 200000,
    cost_per_million_input_tokens_long_ctx          = 2.50,
    cost_per_million_output_tokens_long_ctx         = 15.00,
    cost_per_million_cached_input_tokens_long_ctx   = 0.25,
    cost_per_million_cache_creation_tokens_long_ctx = 0
WHERE provider_name = 'googleai' AND model_name = 'gemini-2.5-pro';

-- gemini-2.5-flash: input $0.30 / output $2.50, cached $0.03, storage $1/M/hr.
UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens     = 0.03,
    cost_per_million_cache_creation_tokens   = 0,
    cost_per_million_cached_storage_per_hour = 1.00
WHERE provider_name = 'googleai' AND model_name IN (
    'gemini-2.5-flash',
    'gemini-2.5-flash-preview-09-2025'
);

-- gemini-2.5-flash-lite (and previews): input $0.10 / output $0.40, cached $0.01, storage $1/M/hr.
UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens     = 0.01,
    cost_per_million_cache_creation_tokens   = 0,
    cost_per_million_cached_storage_per_hour = 1.00
WHERE provider_name = 'googleai' AND model_name IN (
    'gemini-2.5-flash-lite',
    'gemini-2.5-flash-lite-preview-06-17',
    'gemini-2.5-flash-lite-preview-09-2025'
);

-- gemini-2.0-flash and -001: input $0.10 / output $0.40, cached $0.025, storage $1/M/hr.
-- (Note: 2.0-flash cached rate is $0.025 per Google docs, not 10% of input.)
UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens     = 0.025,
    cost_per_million_cache_creation_tokens   = 0,
    cost_per_million_cached_storage_per_hour = 1.00
WHERE provider_name = 'googleai' AND model_name IN (
    'gemini-2.0-flash',
    'gemini-2.0-flash-001'
);

-- gemini-2.0-flash-lite: caching not supported -> leave NULL.
-- (No UPDATE.)

-- gemini-1.5-pro: not on current pricing page; using historical rates.
-- input $1.25 (<=128K) / $2.50 (>128K), cached $0.3125 / $0.625, storage $4.50/M/hr.
UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens            = 0.3125,
    cost_per_million_cache_creation_tokens          = 0,
    cost_per_million_cached_storage_per_hour        = 4.50,
    context_threshold_tokens                        = 128000,
    cost_per_million_input_tokens_long_ctx          = 2.50,
    cost_per_million_output_tokens_long_ctx         = 10.00,
    cost_per_million_cached_input_tokens_long_ctx   = 0.625,
    cost_per_million_cache_creation_tokens_long_ctx = 0
WHERE provider_name = 'googleai' AND model_name = 'gemini-1.5-pro';

-- gemini-1.5-flash: historical rates.
UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens     = 0.01875,
    cost_per_million_cache_creation_tokens   = 0,
    cost_per_million_cached_storage_per_hour = 1.00
WHERE provider_name = 'googleai' AND model_name = 'gemini-1.5-flash';

-- gemini-1.5-flash-8b: historical rates.
UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens     = 0.009375,
    cost_per_million_cache_creation_tokens   = 0,
    cost_per_million_cached_storage_per_hour = 0.25
WHERE provider_name = 'googleai' AND model_name = 'gemini-1.5-flash-8b';

-- gemini-embedding: caching not applicable for embeddings.
-- (No UPDATE.)

-- ============================================================================
-- ANTHROPIC
-- ============================================================================
-- All Anthropic models: cached read = 10% of input, creation = 1.25x input,
-- no storage fee, no long-context tier.

-- claude-haiku-3: input $0.25, cached read $0.025, creation $0.3125.
UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens   = 0.025,
    cost_per_million_cache_creation_tokens = 0.3125
WHERE provider_name = 'anthropic' AND model_name = 'claude-haiku-3';

-- claude-haiku-3.5: input $0.80, cached read $0.08, creation $1.00.
UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens   = 0.08,
    cost_per_million_cache_creation_tokens = 1.00
WHERE provider_name = 'anthropic' AND model_name = 'claude-haiku-3.5';

-- claude-sonnet-3.7 / claude-sonnet-4 / claude-sonnet-4.5: input $3,
-- cached read $0.30, creation $3.75.
UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens   = 0.30,
    cost_per_million_cache_creation_tokens = 3.75
WHERE provider_name = 'anthropic' AND model_name IN (
    'claude-sonnet-3.7',
    'claude-sonnet-4',
    'claude-sonnet-4.5-20250929'
);

-- claude-opus-3 / claude-opus-4 / claude-opus-4-20250514: input $15,
-- cached read $1.50, creation $18.75.
UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens   = 1.50,
    cost_per_million_cache_creation_tokens = 18.75
WHERE provider_name = 'anthropic' AND model_name IN (
    'claude-opus-3',
    'claude-opus-4',
    'claude-opus-4-20250514'
);
