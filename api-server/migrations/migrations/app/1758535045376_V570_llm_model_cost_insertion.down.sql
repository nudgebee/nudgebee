-- Remove model pricing for specified providers
DELETE FROM llm_model_pricing WHERE provider_name IN ('googleai', 'anthropic', 'openai');
