-- Revert V728: clear caching rates and long-context tier columns for the rows
-- this migration touched. Anthropic + all Gemini models with cache support.

UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens            = NULL,
    cost_per_million_cache_creation_tokens          = NULL,
    cost_per_million_cached_storage_per_hour        = NULL,
    context_threshold_tokens                        = NULL,
    cost_per_million_input_tokens_long_ctx          = NULL,
    cost_per_million_output_tokens_long_ctx         = NULL,
    cost_per_million_cached_input_tokens_long_ctx   = NULL,
    cost_per_million_cache_creation_tokens_long_ctx = NULL
WHERE
    (provider_name = 'googleai' AND model_name IN (
        'gemini-3.1-pro-preview',
        'gemini-3-pro-preview',
        'gemini-3-flash-preview',
        'gemini-3.1-flash-lite-preview',
        'gemini-2.5-pro',
        'gemini-2.5-flash',
        'gemini-2.5-flash-preview-09-2025',
        'gemini-2.5-flash-lite',
        'gemini-2.5-flash-lite-preview-06-17',
        'gemini-2.5-flash-lite-preview-09-2025',
        'gemini-2.0-flash',
        'gemini-2.0-flash-001',
        'gemini-1.5-pro',
        'gemini-1.5-flash',
        'gemini-1.5-flash-8b'
    ))
    OR (provider_name = 'anthropic' AND model_name IN (
        'claude-haiku-3',
        'claude-haiku-3.5',
        'claude-sonnet-3.7',
        'claude-sonnet-4',
        'claude-sonnet-4.5-20250929',
        'claude-opus-3',
        'claude-opus-4',
        'claude-opus-4-20250514'
    ));
