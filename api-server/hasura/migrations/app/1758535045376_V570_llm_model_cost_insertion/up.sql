DELETE FROM llm_model_pricing WHERE provider_name = 'googleai';

-- Real Gemini models only
INSERT INTO llm_model_pricing (model_name, provider_name, cost_per_million_input_tokens, cost_per_million_output_tokens) VALUES
-- 2.5 Series
('gemini-2.5-pro', 'googleai', 1.25, 10.00),
('gemini-2.5-flash', 'googleai', 0.30, 2.50),
('gemini-2.5-flash-lite', 'googleai', 0.10, 0.40),
('gemini-2.5-flash-lite-preview-06-17', 'googleai', 0.10, 0.40),

-- 2.0 Series  
('gemini-2.0-flash', 'googleai', 0.10, 0.40),
('gemini-2.0-flash-lite', 'googleai', 0.075, 0.30),

-- 1.5 Series (legacy)
('gemini-1.5-pro', 'googleai', 1.25, 5.00),
('gemini-1.5-flash', 'googleai', 0.075, 0.30),
('gemini-1.5-flash-8b', 'googleai', 0.0375, 0.15),

-- Embedding
('gemini-embedding', 'googleai', 0.15, 0.00);

-- Clear existing Anthropic entries
DELETE FROM llm_model_pricing WHERE provider_name = 'anthropic';

-- Current Anthropic models (September 2025)
INSERT INTO llm_model_pricing (model_name, provider_name, cost_per_million_input_tokens, cost_per_million_output_tokens) VALUES
-- Claude 4 Series (Current flagship)
('claude-opus-4', 'anthropic', 15.00, 75.00),
('claude-sonnet-4', 'anthropic', 3.00, 15.00),
('claude-haiku-3.5', 'anthropic', 0.80, 4.00),

-- Claude 3 Series (Legacy but still available)
('claude-opus-3', 'anthropic', 15.00, 75.00),
('claude-sonnet-3.7', 'anthropic', 3.00, 15.00),
('claude-haiku-3', 'anthropic', 0.25, 1.25);




DELETE FROM llm_model_pricing WHERE provider_name = 'openai';

INSERT INTO llm_model_pricing (model_name, provider_name, cost_per_million_input_tokens, cost_per_million_output_tokens) VALUES
('gpt-5', 'openai', 1.25, 10.00),
('gpt-5-mini', 'openai', 0.25, 2.00),
('gpt-5-nano', 'openai', 0.05, 0.40),
('gpt-4o', 'openai', 2.50, 10.00),
('gpt-4o-mini', 'openai', 0.15, 0.60),
('o3', 'openai', 2.00, 8.00),
('o4-mini', 'openai', 1.10, 4.40);
