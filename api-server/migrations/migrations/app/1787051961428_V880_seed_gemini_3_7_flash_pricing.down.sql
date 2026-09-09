DELETE FROM llm_model_pricing
WHERE tenant_id IS NULL AND provider_name = 'googleai' AND model_name = 'gemini-3.7-flash';
