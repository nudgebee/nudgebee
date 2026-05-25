-- Revert V730: remove embedding pricing rows added by this migration.
DELETE FROM llm_model_pricing
WHERE (provider_name, model_name) IN (
    ('googleai', 'gemini-embedding-001'),
    ('googleai', 'models/gemini-embedding-001'),
    ('googleai', 'text-embedding-004'),
    ('bedrock',  'amazon.titan-embed-text-v2:0')
);
