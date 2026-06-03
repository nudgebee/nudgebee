DELETE FROM llm_model_pricing
WHERE model_name IN (
    'gemini-2.5-flash-preview-09-2025',
    'gemini-2.5-flash-lite-preview-09-2025'
) AND provider_name = 'googleai';
