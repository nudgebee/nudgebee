INSERT INTO llm_model_pricing (
    model_name, 
    provider_name, 
    cost_per_million_input_tokens, 
    cost_per_million_output_tokens, 
    cache_cost_per_million_tokens_per_hour
) VALUES
('gemini-3-pro-preview', 'googleai', 2.00, 12.00, 4.50),
('gemini-3-flash-preview', 'googleai', 0.50, 3.00, 1.00),
('meta.llama4-maverick-17b-instruct-v1:0', 'bedrock', 0.24, 0.97, 0.00),
('us.meta.llama4-maverick-17b-instruct-v1:0', 'bedrock', 0.24, 0.97, 0.00),
('meta.llama4-scout-17b-instruct-v1:0', 'bedrock', 0.17, 0.66, 0.00),
('us.meta.llama4-scout-17b-instruct-v1:0', 'bedrock', 0.17, 0.66, 0.00),
('arn:aws:bedrock:us-west-2:864186153326:inference-profile/us.meta.llama4-maverick-17b-instruct-v1:0', 'bedrock', 0.24, 0.97, 0.00),
('arn:aws:bedrock:us-west-2:864186153326:inference-profile/us.meta.llama4-scout-17b-instruct-v1:0', 'bedrock', 0.17, 0.66, 0.00)
ON CONFLICT (model_name, provider_name)
DO UPDATE SET
    cost_per_million_input_tokens = EXCLUDED.cost_per_million_input_tokens,
    cost_per_million_output_tokens = EXCLUDED.cost_per_million_output_tokens,
    cache_cost_per_million_tokens_per_hour = EXCLUDED.cache_cost_per_million_tokens_per_hour;