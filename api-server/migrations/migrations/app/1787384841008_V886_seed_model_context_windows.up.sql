-- Seed max_context_tokens so the pricing catalog carries model context windows,
-- the way V878 did for max_output_tokens (#36471). Until now the column was set
-- on exactly one row (a tenant's custom vertex model) and every built-in model
-- resolved its window from llm-server's code map instead.
--
-- Why now: gpt-5.6-sol/luna/terra matched no case in that code map and fell to
-- its 32,000 global default. With the catalog's 65,536 output reserve exceeding
-- that window, summarizationChunkSize halved it to a 15,232-token prompt budget
-- and the pre-flight trimmer cut the human turn -- user question, scratchpad and
-- a 50KB kubectl result -- down to its 256-token floor. The model then answered
-- a question it could no longer see. A 1,050,000-token model was running inside
-- 15k. Auditing the rest of the map turned up the same hole elsewhere: gpt-4o
-- matched nothing either (32,000 vs a real 128,000), and every Claude newer than
-- Sonnet 4 fell through to a generic 100,000 case despite shipping 200k or 1M.
--
-- Values below marked [doc] were read from the vendor's current model pages
-- while writing this migration. Values marked [map] are carried over unchanged
-- from llm-server's code map -- no worse than today's behaviour, and a
-- prerequisite for deleting that map -- and should be doc-checked before the
-- map is removed. Embeddings stay NULL: no chat context window applies.
--
-- Only fills NULLs, so an operator override set through the Model Pricing tab is
-- never clobbered. Lock window: trivial -- single UPDATE over ~73 rows.

UPDATE llm_model_pricing SET max_context_tokens = sub.v
FROM (
  SELECT id, CASE
    WHEN model_name ILIKE '%embedding%' THEN NULL

    -- Anthropic [doc]: 1M from the Opus 4.6 generation onward; 200k before it.
    -- Keyed on generation, not model id: a point release that falls through to a
    -- generic case is exactly how this class of bug keeps recurring. Minor
    -- versions allow two digits (4.10), and the trailing ([^0-9]|$) stops a
    -- dated id like claude-opus-4-20250514 matching on its "20".
    WHEN model_name ~* 'claude-(fable|mythos|opus|sonnet)-5\y' THEN 1000000
    WHEN model_name ~* 'claude-(opus|sonnet)-4[.-]([6-9]|[1-9][0-9]{1,2})([^0-9]|$)' THEN 1000000
    WHEN model_name ILIKE '%claude%' THEN 200000

    -- OpenAI [doc]: GPT-5.4/5.5/5.6 all publish 1,050,000; GPT-5 publishes
    -- 400,000; GPT-4o 128,000; o-series 200,000. Specific families first.
    WHEN model_name ~* 'gpt-5[.-]([4-9]|[1-9][0-9]{1,2})([^0-9]|$)' THEN 1050000
    WHEN model_name ILIKE '%gpt-5%' THEN 400000
    WHEN model_name ILIKE '%gpt-4o%' THEN 128000
    WHEN model_name ~* '(^|[./])o[0-9]+(-|$)' THEN 200000

    -- Google [map]
    WHEN model_name ILIKE '%gemini-3-pro%' THEN 2000000
    WHEN model_name ILIKE '%gemini-3%' THEN 1000000
    WHEN model_name ILIKE '%gemini-2.5%' THEN 1000000
    WHEN model_name ILIKE '%gemini-2.0%' THEN 1048576
    WHEN model_name ILIKE '%gemini-1.5-pro%' THEN 2000000
    WHEN model_name ILIKE '%gemini-1.5%' THEN 1000000

    -- Meta [map]
    WHEN model_name ILIKE '%llama4-scout%' THEN 10000000
    WHEN model_name ILIKE '%llama4-maverick%' THEN 1000000
    WHEN model_name ILIKE '%llama3-1-70b%' THEN 131072

    ELSE NULL
  END AS v
  FROM llm_model_pricing
) sub
WHERE llm_model_pricing.id = sub.id
  AND llm_model_pricing.max_context_tokens IS NULL
  AND sub.v IS NOT NULL;

-- Self-hosted models that carry no catalog row at all and therefore resolved
-- BOTH limits from defaults: the code map for context and the 4096 output floor.
-- Together they account for ~60k calls/30d in dev, more than every catalogued
-- model combined, so the two models that most need correct limits had none.
--
-- Context is 262144 for all three, which is each model's own native/default
-- window: Qwen3.6 is 262144 natively (1,010,000 only with YaRN enabled at serve
-- time), Gemma 4 26B publishes 256K, and Nemotron 3 Nano supports 1M but ships a
-- 256k default in its HF/vLLM config. Output: Qwen documents a 32768
-- recommendation; the other two publish no ceiling, so they are left NULL and
-- take the floor, which still logs a WARN naming them.
--
-- IMPORTANT: these are served by vLLM, where the real ceiling is --max-model-len
-- at launch, not the model card. A value above what the server was started with
-- produces hard 400s rather than the silent truncation it replaces. 262144
-- matches what Qwen already resolves to today (unchanged behaviour), but is a
-- RAISE for gemma-4 (8192 via the code map's gemma case) and Nemotron (32000
-- default) -- verify both against the deployed serving args before promoting
-- past dev.
--
-- gemma-4-31b-it is stored WITHOUT the ":free" suffix it is called with. The
-- catalog indexes each row under both its raw name and its canonical form, and
-- canonicalModelID strips ":..." before matching, so the bare row is reached by
-- every suffixed variant -- one row instead of one per marketplace tier.
--
-- Cost is 0: self-hosted, no per-token vendor charge. Columns are NOT NULL, so
-- the row cannot omit them.
INSERT INTO llm_model_pricing (
    model_name, provider_name,
    cost_per_million_input_tokens, cost_per_million_output_tokens,
    max_context_tokens, max_output_tokens) VALUES
    ('Qwen/Qwen3.6-35B-A3B-FP8',            'huggingface', 0, 0, 262144, 32768),
    ('google/gemma-4-26B-A4B-it',           'custom',      0, 0, 262144, NULL),
    ('NVIDIA-Nemotron-3-Nano-30B-A3B-BF16', 'custom',      0, 0, 262144, NULL),
    ('google/gemma-4-31b-it',                'custom',      0, 0, 262144, NULL)
ON CONFLICT (model_name, provider_name) WHERE tenant_id IS NULL
DO UPDATE SET
    max_context_tokens = COALESCE(llm_model_pricing.max_context_tokens, EXCLUDED.max_context_tokens),
    max_output_tokens  = COALESCE(llm_model_pricing.max_output_tokens,  EXCLUDED.max_output_tokens);
