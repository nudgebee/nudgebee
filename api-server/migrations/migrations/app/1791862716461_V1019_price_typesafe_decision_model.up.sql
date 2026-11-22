-- Price for TypeSafe's Jev decision model (#41014). Decision calls are recorded in
-- llm_conversation_token_usage under provider 'typesafe' with the configured model
-- 'jev-latest'; without a row they count as unpriced. $0.042 per million input
-- tokens, output free (TypeSafe's published rate on 2026-10-13). OpenAI Decisions
-- runs on gpt-6-luna, which V984 already prices ($0.10/M input; decision calls
-- record no output tokens).
-- Lock window: only llm_model_pricing is touched (one row).
INSERT INTO llm_model_pricing (model_name, provider_name, tenant_id,
    cost_per_million_input_tokens, cost_per_million_output_tokens)
SELECT 'jev-latest', 'typesafe', NULL, 0.042, 0
WHERE NOT EXISTS (
    SELECT 1 FROM llm_model_pricing p
    WHERE p.tenant_id IS NULL AND p.provider_name = 'typesafe' AND p.model_name = 'jev-latest'
);
