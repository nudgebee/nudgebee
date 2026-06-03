-- Revert V729: clear OpenAI caching rate columns.
UPDATE llm_model_pricing SET
    cost_per_million_cached_input_tokens   = NULL,
    cost_per_million_cache_creation_tokens = NULL
WHERE provider_name = 'openai'
  AND model_name IN (
    'gpt-4o',
    'gpt-4o-mini',
    'gpt-5',
    'gpt-5-mini',
    'gpt-5-nano',
    'o3',
    'o4-mini'
  );
