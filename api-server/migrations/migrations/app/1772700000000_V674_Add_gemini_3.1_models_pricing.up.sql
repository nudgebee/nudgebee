INSERT INTO llm_model_pricing (
    model_name,
    provider_name,
    cost_per_million_input_tokens,
    cost_per_million_output_tokens,
    cache_cost_per_million_tokens_per_hour
) VALUES
('gemini-3.1-pro-preview', 'googleai', 2.00, 12.00, 4.50),
('gemini-3.1-flash-lite-preview', 'googleai', 0.25, 1.50, 1.00)
ON CONFLICT (model_name, provider_name)
DO UPDATE SET
    cost_per_million_input_tokens = EXCLUDED.cost_per_million_input_tokens,
    cost_per_million_output_tokens = EXCLUDED.cost_per_million_output_tokens,
    cache_cost_per_million_tokens_per_hour = EXCLUDED.cache_cost_per_million_tokens_per_hour;
