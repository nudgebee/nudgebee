-- Catalog top-up for the AI Gateway: claude-opus-5, the current Anthropic flagship
-- (launched 2026-07-24), which had no llm_model_pricing row — so gateway traffic to it
-- would meter at $0. It launched at parity with Opus 4.8, so the rates match that row.
--
-- Rates per 1M tokens: input, output, cached-input (read = 10% of input), cache-write
-- (creation = 1.25x input) — the standard Anthropic ratios, identical to claude-opus-4.8.
-- Every other current Claude model is already priced (opus 4.5-4.8, sonnet 5, haiku 4.5,
-- fable 5). Idempotent upsert; existing rows untouched.
INSERT INTO llm_model_pricing (model_name, provider_name,
    cost_per_million_input_tokens, cost_per_million_output_tokens,
    cost_per_million_cached_input_tokens, cost_per_million_cache_creation_tokens) VALUES
    ('claude-opus-5', 'anthropic', 5.00, 25.00, 0.50, 6.25)
ON CONFLICT (model_name, provider_name)
DO UPDATE SET
    cost_per_million_input_tokens = EXCLUDED.cost_per_million_input_tokens,
    cost_per_million_output_tokens = EXCLUDED.cost_per_million_output_tokens,
    cost_per_million_cached_input_tokens = EXCLUDED.cost_per_million_cached_input_tokens,
    cost_per_million_cache_creation_tokens = EXCLUDED.cost_per_million_cache_creation_tokens;
