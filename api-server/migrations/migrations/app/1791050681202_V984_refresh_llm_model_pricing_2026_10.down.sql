-- Revert the catalog refresh. Rows this migration added are deleted; updated rows go back
-- to their previous rates.
DELETE FROM llm_model_pricing
 WHERE tenant_id IS NULL AND (
       (provider_name = 'anthropic' AND model_name IN (
            'claude-opus-5-5', 'claude-sonnet-5-5', 'claude-fable-5-1',
            'claude-opus-4-8', 'claude-opus-4-7', 'claude-opus-4-6', 'claude-opus-4-5',
            'claude-sonnet-4-6', 'claude-sonnet-4-5', 'claude-haiku-4-5'))
    OR (provider_name = 'googleai' AND model_name = 'gemini-3.8-flash')
    OR (provider_name = 'openai' AND model_name IN ('gpt-6-astra', 'gpt-6.1-sol', 'gpt-6-sol', 'gpt-6-luna')));

UPDATE llm_model_pricing
   SET cost_per_million_input_tokens = 3.00, cost_per_million_output_tokens = 15.00,
       cost_per_million_cached_input_tokens = 0.30, cost_per_million_cache_creation_tokens = 3.75
 WHERE tenant_id IS NULL AND provider_name = 'anthropic' AND model_name = 'claude-sonnet-5';

UPDATE llm_model_pricing
   SET cost_per_million_input_tokens = 1.50, cost_per_million_output_tokens = 7.50,
       cost_per_million_cached_input_tokens = 0.15
 WHERE tenant_id IS NULL AND provider_name = 'googleai'
   AND model_name IN ('gemini-3.6-flash', 'gemini-3.7-flash');

UPDATE llm_model_pricing p
   SET cost_per_million_input_tokens = v.inp, cost_per_million_output_tokens = v.outp,
       cost_per_million_cached_input_tokens = v.rd, cost_per_million_cache_creation_tokens = 0,
       context_threshold_tokens = NULL,
       cost_per_million_input_tokens_long_ctx = NULL, cost_per_million_output_tokens_long_ctx = NULL,
       cost_per_million_cached_input_tokens_long_ctx = NULL,
       cost_per_million_cache_creation_tokens_long_ctx = NULL
  FROM (VALUES
    ('gpt-5.6-sol',   5.00, 30.00, 0.50),
    ('gpt-5.6-terra', 2.50, 15.00, 0.25),
    ('gpt-5.6-luna',  1.00,  6.00, 0.10)
  ) AS v(model_name, inp, outp, rd)
 WHERE p.tenant_id IS NULL AND p.provider_name = 'openai' AND p.model_name = v.model_name;
