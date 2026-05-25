-- V730: Backfill embedding model pricing so cost-attribution JOINs against
-- rag_embedding_token_usage stop falling to $0.
--
-- Issue #29972 §F reported the `gemini-embedding` vs `gemini-embedding-001`
-- mismatch (~5.5M tokens / 4d unattributed). DB audit on 2026-05-09 found
-- the gap is wider than reported:
--
--   embedding_model                 rows    tokens
--   gemini-embedding-001           24,797   23.6M  -- §F (matches API name)
--   models/gemini-embedding-001     5,266   44.5M  -- §F (LangChain "models/" prefix)
--   text-embedding-004             13,421  180.0M  -- not in pricing at all
--   amazon.titan-embed-text-v2:0        8   23.0K  -- not in pricing at all
--
-- Different libraries legitimately use different naming for the same Gemini
-- embedding endpoint (raw API vs LangChain wrapper); we add both rather than
-- normalize at write time so historical rows JOIN cleanly without backfill.
--
-- The legacy `gemini-embedding` row (no version suffix) is left in place;
-- removing it risks breaking any code path that may still write it. It can
-- be dropped in a later migration once usage reaches zero.
--
-- Sources (verified 2026-05-09):
--   Gemini embedding pricing: https://ai.google.dev/gemini-api/docs/pricing
--   Vertex text-embedding-004 legacy rate: $0.000025/1K char ~= $0.025/M tokens
--   AWS Titan Embeddings v2: https://aws.amazon.com/bedrock/pricing/
--     ($0.00002/1K input tokens = $0.02/M)

INSERT INTO llm_model_pricing (
    provider_name,
    model_name,
    cost_per_million_input_tokens,
    cost_per_million_output_tokens
) VALUES
    ('googleai', 'gemini-embedding-001',          0.15,   0),
    ('googleai', 'models/gemini-embedding-001',   0.15,   0),
    ('googleai', 'text-embedding-004',            0.025,  0),
    ('bedrock',  'amazon.titan-embed-text-v2:0',  0.02,   0)
ON CONFLICT (model_name, provider_name)
DO UPDATE SET
    cost_per_million_input_tokens  = EXCLUDED.cost_per_million_input_tokens,
    cost_per_million_output_tokens = EXCLUDED.cost_per_million_output_tokens;
