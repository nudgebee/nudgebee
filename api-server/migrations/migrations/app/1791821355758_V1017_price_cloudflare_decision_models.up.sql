-- Prices for the Cloudflare decision models (#40902). Decision calls are recorded in
-- llm_conversation_token_usage with provider 'cloudflare', but without a price row they
-- count as unpriced (cost $0). Rates from Cloudflare's Workers AI pricing page on
-- 2026-10-12: input per million tokens; output is free.
-- Lock window: only llm_model_pricing is touched (two rows).
INSERT INTO llm_model_pricing (model_name, provider_name, tenant_id,
    cost_per_million_input_tokens, cost_per_million_output_tokens)
SELECT v.model_name, 'cloudflare', NULL, v.inp, 0
FROM (VALUES
    ('clef',       0.24),
    ('clef-flash', 0.038)
) AS v(model_name, inp)
WHERE NOT EXISTS (
    SELECT 1 FROM llm_model_pricing p
    WHERE p.tenant_id IS NULL AND p.provider_name = 'cloudflare' AND p.model_name = v.model_name
);
