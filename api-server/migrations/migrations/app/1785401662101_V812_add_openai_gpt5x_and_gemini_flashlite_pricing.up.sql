-- Catalog top-up for the AI Gateway, from providers' current pricing pages
-- (cross-checked): OpenAI's current GPT text lineup (gpt-5.4/5.5/5.6 families) plus
-- the GA alias of a Gemini model already present only as a preview.
--
-- Columns: input, output, cache read (cached input), cache write (creation) per 1M tok.
--   * OpenAI has no separate cache-write charge (automatic caching), so cache-creation
--     is 0 for every OpenAI row — matching the existing gpt-4o/gpt-5 rows. The "pro"
--     variants publish no DISCOUNTED cached-input rate, so their cache-read is set to the
--     standard input rate (30) — cached tokens bill at full input price rather than $0,
--     which never undercharges (and is a no-op if the model doesn't cache).
--   * gemini-3.1-flash-lite is the GA name for gemini-3.1-flash-lite-preview (already in
--     the catalog); same rates.
-- Scope: GPT text/chat models only (realtime, codex, and embeddings intentionally left
-- out). Idempotent upsert; deprecated/existing rows are untouched.
INSERT INTO llm_model_pricing (model_name, provider_name,
    cost_per_million_input_tokens, cost_per_million_output_tokens,
    cost_per_million_cached_input_tokens, cost_per_million_cache_creation_tokens) VALUES
    ('gpt-5.6-sol',   'openai',  5.00,  30.00, 0.50,  0),
    ('gpt-5.6-terra', 'openai',  2.50,  15.00, 0.25,  0),
    ('gpt-5.6-luna',  'openai',  1.00,   6.00, 0.10,  0),
    ('gpt-5.5',       'openai',  5.00,  30.00, 0.50,  0),
    ('gpt-5.5-pro',   'openai', 30.00, 180.00, 30.00, 0),
    ('gpt-5.4',       'openai',  2.50,  15.00, 0.25,  0),
    ('gpt-5.4-mini',  'openai',  0.75,   4.50, 0.075, 0),
    ('gpt-5.4-nano',  'openai',  0.20,   1.25, 0.02,  0),
    ('gpt-5.4-pro',   'openai', 30.00, 180.00, 30.00, 0),
    ('gemini-3.1-flash-lite', 'googleai', 0.25, 1.50, 0.025, 0)
ON CONFLICT (model_name, provider_name)
DO UPDATE SET
    cost_per_million_input_tokens = EXCLUDED.cost_per_million_input_tokens,
    cost_per_million_output_tokens = EXCLUDED.cost_per_million_output_tokens,
    cost_per_million_cached_input_tokens = EXCLUDED.cost_per_million_cached_input_tokens,
    cost_per_million_cache_creation_tokens = EXCLUDED.cost_per_million_cache_creation_tokens;
