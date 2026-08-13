-- Pricing should key off the NORMALIZED model name (undated, dotted) so a model
-- prices correctly regardless of the snapshot date a client sends. The pricer strips
-- any -YYYYMMDD suffix and converts single-digit dash versions to dots
-- (claude-haiku-4-5-20251001 -> claude-haiku-4.5), so one normalized row covers every
-- dated/undated variant of a model family. This matches the existing
-- claude-haiku-3.5 / claude-sonnet-3.7 rows.
--
-- 1) claude-sonnet-4.6 — was entirely missing from the catalog (cost computed as $0).
-- 2) claude-haiku-4.5  — the catalog had only the exact-dated claude-haiku-4-5-20251001
--    (V784), which silently reverts to $0 for any OTHER Haiku 4.5 snapshot date or the
--    undated alias. Add the normalized row so all variants price. The dated V784 row is
--    left in place — it's a harmless exact match at the same rate.
--
-- Rates: Sonnet $3 in / $15 out per 1M (cache read $0.30, 5m-write $3.75); Haiku $1 in /
-- $5 out (cache read $0.10, 5m-write $1.25) — identical to their existing catalog rows.
INSERT INTO llm_model_pricing (model_name, provider_name,
    cost_per_million_input_tokens, cost_per_million_output_tokens,
    cost_per_million_cached_input_tokens, cost_per_million_cache_creation_tokens) VALUES
('claude-sonnet-4.6', 'anthropic', 3.00, 15.00, 0.30, 3.75),
('claude-haiku-4.5',  'anthropic', 1.00,  5.00, 0.10, 1.25)
ON CONFLICT (model_name, provider_name)
DO UPDATE SET
    cost_per_million_input_tokens = EXCLUDED.cost_per_million_input_tokens,
    cost_per_million_output_tokens = EXCLUDED.cost_per_million_output_tokens,
    cost_per_million_cached_input_tokens = EXCLUDED.cost_per_million_cached_input_tokens,
    cost_per_million_cache_creation_tokens = EXCLUDED.cost_per_million_cache_creation_tokens;
