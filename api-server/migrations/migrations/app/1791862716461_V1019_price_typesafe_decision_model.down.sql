DELETE FROM llm_model_pricing
 WHERE tenant_id IS NULL AND provider_name = 'typesafe' AND model_name = 'jev-latest';
