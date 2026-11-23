DELETE FROM llm_model_pricing
 WHERE tenant_id IS NULL AND provider_name = 'cloudflare' AND model_name IN ('clef', 'clef-flash');
