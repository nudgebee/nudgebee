-- Claude Haiku 4.5 (claude-haiku-4-5-20251001) was missing from the pricing
-- catalog, so gateway/LLM-Analyser cost for that model computed as $0. Seed it
-- with Anthropic's published rates ($1 in / $5 out per 1M; cache read $0.10,
-- cache 5m-write $1.25). Matches the existing per-model pricing migrations
-- (e.g. V574 opus-4, V674 gemini-3.1).
INSERT INTO llm_model_pricing (model_name, provider_name,
    cost_per_million_input_tokens, cost_per_million_output_tokens,
    cost_per_million_cached_input_tokens, cost_per_million_cache_creation_tokens) VALUES
('claude-haiku-4-5-20251001', 'anthropic', 1.00, 5.00, 0.10, 1.25)
ON CONFLICT (model_name, provider_name)
DO UPDATE SET
    cost_per_million_input_tokens = EXCLUDED.cost_per_million_input_tokens,
    cost_per_million_output_tokens = EXCLUDED.cost_per_million_output_tokens,
    cost_per_million_cached_input_tokens = EXCLUDED.cost_per_million_cached_input_tokens,
    cost_per_million_cache_creation_tokens = EXCLUDED.cost_per_million_cache_creation_tokens;
