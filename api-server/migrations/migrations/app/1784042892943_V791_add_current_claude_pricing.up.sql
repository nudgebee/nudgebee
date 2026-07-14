-- Authoritative current Claude API pricing, from Anthropic's pricing page
-- (platform.claude.com/docs/en/docs/about-claude/pricing), seeded under the
-- NORMALIZED (undated, dotted) model name so every dated/undated snapshot a client
-- sends prices correctly (the gateway pricer strips -YYYYMMDD and converts single-
-- digit dash versions to dots — e.g. claude-opus-4-8 -> claude-opus-4.8). Idempotent
-- upsert, so re-seeding already-present rows (sonnet-4.6, haiku-4.5) is a no-op.
--
-- Columns map to: input, output, cache read (hit), 5-minute cache write (per 1M tok).
-- Notes:
--   * Motivating gap: claude-opus-4-8 (Claude CLI) had no row → cost computed as $0.
--   * claude-sonnet-5 seeded at STANDARD $3/$15 (not the $2/$10 introductory rate in
--     effect through 2026-08-31) — the catalog has no time dimension and the pricer is
--     documented to err conservative (never under-charge). Revisit if a time-aware
--     price is added.
--   * Deprecated/retired rows already in the catalog (opus-4.1, opus-4, sonnet-4,
--     haiku-3.5) keep their existing rates and are not touched here.
INSERT INTO llm_model_pricing (model_name, provider_name,
    cost_per_million_input_tokens, cost_per_million_output_tokens,
    cost_per_million_cached_input_tokens, cost_per_million_cache_creation_tokens) VALUES
    ('claude-fable-5',   'anthropic', 10.00, 50.00, 1.00, 12.50),
    ('claude-opus-4.8',  'anthropic',  5.00, 25.00, 0.50,  6.25),
    ('claude-opus-4.7',  'anthropic',  5.00, 25.00, 0.50,  6.25),
    ('claude-opus-4.6',  'anthropic',  5.00, 25.00, 0.50,  6.25),
    ('claude-opus-4.5',  'anthropic',  5.00, 25.00, 0.50,  6.25),
    ('claude-sonnet-5',  'anthropic',  3.00, 15.00, 0.30,  3.75),
    ('claude-sonnet-4.6','anthropic',  3.00, 15.00, 0.30,  3.75),
    ('claude-sonnet-4.5','anthropic',  3.00, 15.00, 0.30,  3.75),
    ('claude-haiku-4.5', 'anthropic',  1.00,  5.00, 0.10,  1.25)
ON CONFLICT (model_name, provider_name)
DO UPDATE SET
    cost_per_million_input_tokens = EXCLUDED.cost_per_million_input_tokens,
    cost_per_million_output_tokens = EXCLUDED.cost_per_million_output_tokens,
    cost_per_million_cached_input_tokens = EXCLUDED.cost_per_million_cached_input_tokens,
    cost_per_million_cache_creation_tokens = EXCLUDED.cost_per_million_cache_creation_tokens;
