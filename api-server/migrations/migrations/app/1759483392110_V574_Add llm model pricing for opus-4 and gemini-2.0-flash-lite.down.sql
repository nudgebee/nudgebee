DELETE FROM llm_model_pricing 
WHERE (model_name, provider_name) IN (
    ('claude-opus-4-20250514', 'anthropic'),
    ('gemini-2.0-flash-001', 'googleai'),
    ('claude-sonnet-4.5-20250929', 'anthropic')
);