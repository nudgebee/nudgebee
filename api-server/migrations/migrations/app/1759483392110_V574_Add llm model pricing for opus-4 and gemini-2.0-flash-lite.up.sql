INSERT INTO llm_model_pricing (model_name, provider_name, cost_per_million_input_tokens, cost_per_million_output_tokens) VALUES
('claude-opus-4-20250514', 'anthropic', 15.00, 75.00),
('gemini-2.0-flash-001', 'googleai', 0.10, 0.40),
('claude-sonnet-4.5-20250929', 'anthropic', 3.00, 15.00)
ON CONFLICT (model_name, provider_name) 
DO UPDATE SET 
    cost_per_million_input_tokens = EXCLUDED.cost_per_million_input_tokens,
    cost_per_million_output_tokens = EXCLUDED.cost_per_million_output_tokens;