INSERT INTO llm_model_pricing (model_name, provider_name, cost_per_million_input_tokens, cost_per_million_output_tokens) VALUES
('gemini-2.5-flash-preview-09-2025', 'googleai', 0.30, 2.50),
('gemini-2.5-flash-lite-preview-09-2025', 'googleai', 0.10, 0.40)
ON CONFLICT (model_name, provider_name)
DO UPDATE SET
    cost_per_million_input_tokens = EXCLUDED.cost_per_million_input_tokens,
    cost_per_million_output_tokens = EXCLUDED.cost_per_million_output_tokens;
