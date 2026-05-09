DELETE FROM llm_model_pricing
WHERE model_name IN (
    'gemini-3.1-pro-preview',
    'gemini-3.1-flash-lite-preview'
)
AND provider_name = 'googleai';
