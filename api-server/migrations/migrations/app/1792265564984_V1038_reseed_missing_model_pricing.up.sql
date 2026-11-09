-- Re-seed built-in price rows that some installs never received, and price Claude Haiku 5.5 (#39917).
--
-- The test database has none of the rows V791 (Claude catalog) and V792 (OpenAI GPT-5.x,
-- gemini-3.1-flash-lite) insert, nor the dashed Claude copies V984 derives from them. Both
-- merged on 2026-07-14, the day of the golang-migrate -> Atlas cutover (#33008), and were most
-- likely recorded as applied there without running. Every call on those models stored
-- cost_usd = NULL ("No price"), and with no row there is also no max_output_tokens, so calls
-- fell back to a 4096-token output cap. Rates and limits below copy dev, where the rows exist.
--
-- WHERE NOT EXISTS: an install that already has a built-in row keeps it, and tenant overrides
-- are never touched, so this is a no-op on dev and on any install where V791/V792 ran.
--
-- New rows: Claude Haiku 5.5 ($0.10 / $0.50 per MTok up to a 100K-token prompt, $0.50 / $2.50
-- above; cache reads 0.1x input, 5-minute cache writes 1.25x), and the still-served Anthropic
-- model IDs that had no row anywhere: Mythos 5 / 5.1 (priced as Fable 5 / 5.1), the dated
-- Opus 4.5 / Sonnet 4.5 IDs, and the deprecated Sonnet 4 / Opus 4 aliases. Retired models
-- (Opus 4.1, the Claude 3 family) are left out.
--
-- Lock window: only llm_model_pricing is touched (a few dozen rows).
INSERT INTO llm_model_pricing (model_name, provider_name, tenant_id,
    cost_per_million_input_tokens, cost_per_million_output_tokens,
    cost_per_million_cached_input_tokens, cost_per_million_cache_creation_tokens,
    max_output_tokens, max_context_tokens, context_threshold_tokens,
    cost_per_million_input_tokens_long_ctx, cost_per_million_output_tokens_long_ctx,
    cost_per_million_cached_input_tokens_long_ctx, cost_per_million_cache_creation_tokens_long_ctx)
SELECT v.model_name, v.provider_name, NULL, v.inp, v.outp, v.rd, v.cre, v.max_out, v.max_ctx,
       v.threshold, v.inp_long, v.outp_long, v.rd_long, v.cre_long
FROM (VALUES
    -- Anthropic (V791, plus the dashed IDs the API reports, V984)
    ('claude-fable-5',        'anthropic', 10.00, 50.00, 1.00,  12.50, 65536, 1000000, NULL::int4, NULL::float8, NULL::float8, NULL::float8, NULL::float8),
    ('claude-opus-4-8',       'anthropic',  5.00, 25.00, 0.50,   6.25, 65536, 1000000, NULL, NULL, NULL, NULL, NULL),
    ('claude-opus-4.8',       'anthropic',  5.00, 25.00, 0.50,   6.25, 65536, 1000000, NULL, NULL, NULL, NULL, NULL),
    ('claude-opus-4-7',       'anthropic',  5.00, 25.00, 0.50,   6.25, 65536, 1000000, NULL, NULL, NULL, NULL, NULL),
    ('claude-opus-4.7',       'anthropic',  5.00, 25.00, 0.50,   6.25, 65536, 1000000, NULL, NULL, NULL, NULL, NULL),
    ('claude-opus-4-6',       'anthropic',  5.00, 25.00, 0.50,   6.25, 65536, 1000000, NULL, NULL, NULL, NULL, NULL),
    ('claude-opus-4.6',       'anthropic',  5.00, 25.00, 0.50,   6.25, 65536, 1000000, NULL, NULL, NULL, NULL, NULL),
    ('claude-opus-4-5',       'anthropic',  5.00, 25.00, 0.50,   6.25, 64000,  200000, NULL, NULL, NULL, NULL, NULL),
    ('claude-opus-4.5',       'anthropic',  5.00, 25.00, 0.50,   6.25, 64000,  200000, NULL, NULL, NULL, NULL, NULL),
    ('claude-sonnet-5',       'anthropic',  2.00, 10.00, 0.20,   2.50, 65536, 1000000, NULL, NULL, NULL, NULL, NULL),
    ('claude-sonnet-4-6',     'anthropic',  3.00, 15.00, 0.30,   3.75, 65536, 1000000, NULL, NULL, NULL, NULL, NULL),
    ('claude-sonnet-4.6',     'anthropic',  3.00, 15.00, 0.30,   3.75, 65536, 1000000, NULL, NULL, NULL, NULL, NULL),
    ('claude-sonnet-4-5',     'anthropic',  3.00, 15.00, 0.30,   3.75, 64000,  200000, NULL, NULL, NULL, NULL, NULL),
    ('claude-sonnet-4.5',     'anthropic',  3.00, 15.00, 0.30,   3.75, 64000,  200000, NULL, NULL, NULL, NULL, NULL),
    ('claude-haiku-4-5',      'anthropic',  1.00,  5.00, 0.10,   1.25, 64000,  200000, NULL, NULL, NULL, NULL, NULL),
    ('claude-haiku-4.5',      'anthropic',  1.00,  5.00, 0.10,   1.25, 64000,  200000, NULL, NULL, NULL, NULL, NULL),
    -- Anthropic (new): Haiku 5.5, and still-served model IDs no install had a row for
    ('claude-haiku-5-5',      'anthropic',  0.10,  0.50, 0.01,  0.125, 65536, 1000000, 100000, 0.50, 2.50, 0.05, 0.625),
    ('claude-mythos-5-1',     'anthropic', 10.00, 50.00, 0.25,  12.50, 65536, 1000000, NULL, NULL, NULL, NULL, NULL),
    ('claude-mythos-5',       'anthropic', 10.00, 50.00, 1.00,  12.50, 65536, 1000000, NULL, NULL, NULL, NULL, NULL),
    ('claude-opus-4-5-20251101',   'anthropic', 5.00, 25.00, 0.50, 6.25, 64000, 200000, NULL, NULL, NULL, NULL, NULL),
    ('claude-sonnet-4-5-20250929', 'anthropic', 3.00, 15.00, 0.30, 3.75, 64000, 200000, NULL, NULL, NULL, NULL, NULL),
    ('claude-sonnet-4-0',          'anthropic', 3.00, 15.00, 0.30, 3.75, 64000, 200000, NULL, NULL, NULL, NULL, NULL),
    ('claude-sonnet-4-20250514',   'anthropic', 3.00, 15.00, 0.30, 3.75, 64000, 200000, NULL, NULL, NULL, NULL, NULL),
    ('claude-opus-4-0',           'anthropic', 15.00, 75.00, 1.50, 18.75, 32000, 200000, NULL, NULL, NULL, NULL, NULL),
    -- OpenAI and Google (V792)
    ('gpt-5.4',               'openai',     2.50, 15.00, 0.25,   0.00, 65536, 1050000, NULL, NULL, NULL, NULL, NULL),
    ('gpt-5.4-mini',          'openai',     0.75,  4.50, 0.075,  0.00, 65536, 1050000, NULL, NULL, NULL, NULL, NULL),
    ('gpt-5.4-nano',          'openai',     0.20,  1.25, 0.02,   0.00, 65536, 1050000, NULL, NULL, NULL, NULL, NULL),
    ('gpt-5.4-pro',           'openai',    30.00, 180.00, 30.00, 0.00, 65536, 1050000, NULL, NULL, NULL, NULL, NULL),
    ('gpt-5.5',               'openai',     5.00, 30.00, 0.50,   0.00, 65536, 1050000, NULL, NULL, NULL, NULL, NULL),
    ('gpt-5.5-pro',           'openai',    30.00, 180.00, 30.00, 0.00, 65536, 1050000, NULL, NULL, NULL, NULL, NULL),
    ('gpt-5.6-luna',          'openai',     0.20,  1.20, 0.02,   0.25, 65536, 1050000, 272000, 0.40,  1.80, 0.04,  0.50),
    ('gpt-5.6-sol',           'openai',     4.00, 20.00, 0.40,   5.00, 65536, 1050000, 272000, 8.00, 30.00, 0.80, 10.00),
    ('gpt-5.6-terra',         'openai',     2.00, 12.00, 0.20,   2.50, 65536, 1050000, 272000, 4.00, 18.00, 0.40,  5.00),
    ('gemini-3.1-flash-lite', 'googleai',   0.25,  1.50, 0.025,  0.00, 65536, 1000000, NULL, NULL, NULL, NULL, NULL)
) AS v(model_name, provider_name, inp, outp, rd, cre, max_out, max_ctx,
       threshold, inp_long, outp_long, rd_long, cre_long)
WHERE NOT EXISTS (
    SELECT 1 FROM llm_model_pricing p
    WHERE p.tenant_id IS NULL AND p.provider_name = v.provider_name AND p.model_name = v.model_name
);
