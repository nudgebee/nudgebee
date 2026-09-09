-- Model token ceilings move from llm-server code tables into the pricing
-- catalog so they are editable without a release (#36471). NULL means
-- "unknown" — llm-server falls back to its conservative floor (4096 output /
-- code model map for context) and warns. Lock window: trivial — ~70 rows.

ALTER TABLE llm_model_pricing ADD COLUMN IF NOT EXISTS max_output_tokens integer;
ALTER TABLE llm_model_pricing ADD COLUMN IF NOT EXISTS max_context_tokens integer;

-- Seed max_output_tokens from the values previously hardcoded in llm-server's
-- GetLlmMaxOutputTokens (PR #36449 documents the sources), refined per model
-- now that rows are per-model: #36449's single code case forced the whole
-- Claude 4.0-4.5 band to its lowest ceiling (32000, Opus 4/4.1); here
-- Sonnet 4/4.5, Haiku 4.5 and Opus 4.5 get their documented 64000 while
-- Opus 4/4.1 keeps 32000. 4.6+ and 5.x stay at 65536 (half the documented
-- 128k, same runaway bound as #36449). CASE order matters: specific families
-- before their prefixes. Embeddings, llama-4 and other self-hosted models are
-- deliberately left NULL — unknown or deployment-specific; guessing would
-- turn slow calls into hard 400s. Verified against every built-in row in dev
-- before merging.
UPDATE llm_model_pricing SET max_output_tokens = sub.v
FROM (
  SELECT id, CASE
    WHEN model_name ILIKE '%embedding%' THEN NULL
    WHEN model_name ~* 'claude(-(opus|sonnet|haiku))?-3[.-]5' THEN 8192
    WHEN model_name ~* 'claude(-(opus|sonnet|haiku))?-3(\y|[.-])' THEN 4096
    WHEN model_name ~* 'claude(-(opus|sonnet|haiku))?-4[.-][6-9]' THEN 65536
    WHEN model_name ~* 'claude(-(opus|sonnet|haiku))?-4[.-]5' THEN 64000
    WHEN model_name ~* 'claude-(sonnet|haiku)-4(\y|[.-])' THEN 64000
    WHEN model_name ~* 'claude(-opus)?-4(\y|[.-])' THEN 32000 -- Opus 4/4.1
    WHEN model_name ILIKE '%claude%' THEN 65536 -- 5.x and newer generations
    WHEN model_name ILIKE '%gemini-3%'
      OR model_name ILIKE '%gemini-2.5%'
      OR model_name ILIKE '%gemini-2-5%' THEN 65536
    WHEN model_name ILIKE '%gemini%' THEN 8192
    WHEN model_name ILIKE '%gpt-5%' THEN 65536
    WHEN model_name ~* '(^|[./])o[0-9]+(-|$)' THEN 65536 -- OpenAI o-series
    WHEN model_name ILIKE '%gpt-4o%' THEN 16384
    WHEN model_name ILIKE '%gpt-4%' THEN 4096
    WHEN model_name ILIKE '%llama-3%'
      OR model_name ILIKE '%llama3%' THEN 8192
    WHEN model_name ILIKE '%deepseek%' THEN 8192
    ELSE NULL
  END AS v
  FROM llm_model_pricing
) sub
WHERE llm_model_pricing.id = sub.id
  AND llm_model_pricing.max_output_tokens IS NULL
  AND sub.v IS NOT NULL;
