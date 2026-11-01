-- Catalog refresh (#39633): models with live traffic but no price row record cost_usd = NULL,
-- and a few rows carry stale rates. Rates from the providers' pricing pages on 2026-10-03.
-- Rows that never had a stored cost need no backfill: every report reads
-- COALESCE(cost_usd, <live price>), so they price themselves once their row exists.
-- Past calls stored at a stale rate (a few hundred Sonnet 5 / GPT-5.6 rows) keep their
-- stored cost: re-pricing them would need a full scan of llm_conversation_token_usage,
-- which has no index on provider/model - too risky for an in-deploy migration.
-- Lock window: only llm_model_pricing is touched (a few dozen rows).

-- Anthropic: new models (cache write = 1.25x input; reads 0.1x, 0.05x on Opus 5.5,
-- 0.025x on Fable 5.1). Output capped at 65536 like the other 128K-output Claude rows.
INSERT INTO llm_model_pricing (model_name, provider_name, tenant_id,
    cost_per_million_input_tokens, cost_per_million_output_tokens,
    cost_per_million_cached_input_tokens, cost_per_million_cache_creation_tokens,
    max_output_tokens, max_context_tokens)
SELECT v.model_name, 'anthropic', NULL, v.inp, v.outp, v.rd, v.cre, 65536, 1000000
FROM (VALUES
    ('claude-opus-5-5',   4.00, 20.00, 0.20,  5.00),
    ('claude-sonnet-5-5', 2.00, 10.00, 0.20,  2.50),
    ('claude-fable-5-1', 10.00, 50.00, 0.25, 12.50)
) AS v(model_name, inp, outp, rd, cre)
WHERE NOT EXISTS (
    SELECT 1 FROM llm_model_pricing p
    WHERE p.tenant_id IS NULL AND p.provider_name = 'anthropic' AND p.model_name = v.model_name
);

-- Anthropic: the API reports dashed model IDs (claude-sonnet-4-6) but these models were
-- priced under dotted names (claude-sonnet-4.6), and pricing matches model_name exactly -
-- 390 claude-sonnet-4-6 calls on dev were unpriced. Add each dashed ID as a copy of its
-- dotted row.
INSERT INTO llm_model_pricing (model_name, provider_name, tenant_id,
    cost_per_million_input_tokens, cost_per_million_output_tokens,
    cost_per_million_cached_input_tokens, cost_per_million_cache_creation_tokens,
    max_output_tokens, max_context_tokens)
SELECT replace(d.model_name, '.', '-'), d.provider_name, NULL,
    d.cost_per_million_input_tokens, d.cost_per_million_output_tokens,
    d.cost_per_million_cached_input_tokens, d.cost_per_million_cache_creation_tokens,
    d.max_output_tokens, d.max_context_tokens
FROM llm_model_pricing d
WHERE d.tenant_id IS NULL AND d.provider_name = 'anthropic'
  AND d.model_name IN ('claude-opus-4.8', 'claude-opus-4.7', 'claude-opus-4.6', 'claude-opus-4.5',
                       'claude-sonnet-4.6', 'claude-sonnet-4.5', 'claude-haiku-4.5')
  AND NOT EXISTS (
    SELECT 1 FROM llm_model_pricing p
    WHERE p.tenant_id IS NULL AND p.provider_name = 'anthropic'
      AND p.model_name = replace(d.model_name, '.', '-')
  );

-- Anthropic: Claude Sonnet 5's $2/$10 launch price became its standard price; the
-- scheduled move to $3/$15 was cancelled.
UPDATE llm_model_pricing
   SET cost_per_million_input_tokens = 2.00, cost_per_million_output_tokens = 10.00,
       cost_per_million_cached_input_tokens = 0.20, cost_per_million_cache_creation_tokens = 2.50
 WHERE tenant_id IS NULL AND provider_name = 'anthropic' AND model_name = 'claude-sonnet-5';

-- Google: 3.6/3.7/3.8 Flash bill $0.75 / $3.75 (cache $0.075) through 2026-12-31 and
-- $1.50 / $7.50 (cache $0.15) from 2027-01-01. Rows carried the 2027 price, over-reporting
-- today's cost 2x; set today's price. A follow-up migration restores the 2027 price.
INSERT INTO llm_model_pricing (model_name, provider_name, tenant_id,
    cost_per_million_input_tokens, cost_per_million_output_tokens,
    cost_per_million_cached_input_tokens, cost_per_million_cache_creation_tokens,
    max_output_tokens, max_context_tokens, supports_image_input)
SELECT 'gemini-3.8-flash', 'googleai', NULL, 0.75, 3.75, 0.075, 0,
    max_output_tokens, max_context_tokens, supports_image_input
FROM llm_model_pricing
WHERE tenant_id IS NULL AND provider_name = 'googleai' AND model_name = 'gemini-3.7-flash'
  AND NOT EXISTS (
    SELECT 1 FROM llm_model_pricing p
    WHERE p.tenant_id IS NULL AND p.provider_name = 'googleai' AND p.model_name = 'gemini-3.8-flash'
  );

UPDATE llm_model_pricing
   SET cost_per_million_input_tokens = 0.75, cost_per_million_output_tokens = 3.75,
       cost_per_million_cached_input_tokens = 0.075
 WHERE tenant_id IS NULL AND provider_name = 'googleai'
   AND model_name IN ('gemini-3.6-flash', 'gemini-3.7-flash');

-- OpenAI: GPT-5.6 rows carried pre-launch rates, and the GPT-6 family is new. Standard
-- tier; inputs over 272K tokens bill at the long-context rates.
INSERT INTO llm_model_pricing (model_name, provider_name, tenant_id,
    cost_per_million_input_tokens, cost_per_million_output_tokens,
    cost_per_million_cached_input_tokens, cost_per_million_cache_creation_tokens,
    context_threshold_tokens,
    cost_per_million_input_tokens_long_ctx, cost_per_million_output_tokens_long_ctx,
    cost_per_million_cached_input_tokens_long_ctx, cost_per_million_cache_creation_tokens_long_ctx,
    max_output_tokens, max_context_tokens)
SELECT v.model_name, 'openai', NULL, v.inp, v.outp, v.rd, v.cre, 272000,
    v.inp_l, v.outp_l, v.rd_l, v.cre_l, 65536, 1050000
FROM (VALUES
    ('gpt-6-astra', 10.00, 50.00, 1.00, 12.50,  20.00, 75.00, 2.00, 25.00),
    ('gpt-6.1-sol',  2.00, 10.00, 0.10,  2.50,   4.00, 15.00, 0.20,  5.00),
    ('gpt-6-sol',    2.00, 10.00, 0.20,  2.50,   4.00, 15.00, 0.40,  5.00),
    ('gpt-6-luna',   0.10,  0.50, 0.01,  0.125,  0.20,  0.75, 0.02,  0.25)
) AS v(model_name, inp, outp, rd, cre, inp_l, outp_l, rd_l, cre_l)
WHERE NOT EXISTS (
    SELECT 1 FROM llm_model_pricing p
    WHERE p.tenant_id IS NULL AND p.provider_name = 'openai' AND p.model_name = v.model_name
);

UPDATE llm_model_pricing p
   SET cost_per_million_input_tokens = v.inp, cost_per_million_output_tokens = v.outp,
       cost_per_million_cached_input_tokens = v.rd, cost_per_million_cache_creation_tokens = v.cre,
       context_threshold_tokens = 272000,
       cost_per_million_input_tokens_long_ctx = v.inp_l, cost_per_million_output_tokens_long_ctx = v.outp_l,
       cost_per_million_cached_input_tokens_long_ctx = v.rd_l,
       cost_per_million_cache_creation_tokens_long_ctx = v.cre_l
  FROM (VALUES
    ('gpt-5.6-sol',   4.00, 20.00, 0.40, 5.00,  8.00, 30.00, 0.80, 10.00),
    ('gpt-5.6-terra', 2.00, 12.00, 0.20, 2.50,  4.00, 18.00, 0.40,  5.00),
    ('gpt-5.6-luna',  0.20,  1.20, 0.02, 0.25,  0.40,  1.80, 0.04,  0.50)
  ) AS v(model_name, inp, outp, rd, cre, inp_l, outp_l, rd_l, cre_l)
 WHERE p.tenant_id IS NULL AND p.provider_name = 'openai' AND p.model_name = v.model_name;

