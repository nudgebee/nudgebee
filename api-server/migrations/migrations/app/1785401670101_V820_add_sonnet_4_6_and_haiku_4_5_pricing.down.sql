DELETE FROM llm_model_pricing
WHERE provider_name = 'anthropic'
  AND model_name IN ('claude-sonnet-4.6', 'claude-haiku-4.5');
