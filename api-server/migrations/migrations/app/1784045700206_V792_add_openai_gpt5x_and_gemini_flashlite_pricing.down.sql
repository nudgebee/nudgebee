DELETE FROM llm_model_pricing
WHERE (provider_name = 'openai' AND model_name IN (
        'gpt-5.6-sol', 'gpt-5.6-terra', 'gpt-5.6-luna',
        'gpt-5.5', 'gpt-5.5-pro',
        'gpt-5.4', 'gpt-5.4-mini', 'gpt-5.4-nano', 'gpt-5.4-pro'))
   OR (provider_name = 'googleai' AND model_name = 'gemini-3.1-flash-lite');
