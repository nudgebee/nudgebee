-- Snapshot request cost per row so historical spend is immutable — a later price
-- change never retroactively rewrites it. The gateway computes cost_usd at write time
-- from the resolved model + actual tokens; the dashboard now SUMs this column instead
-- of re-joining the pricing catalog on every read.
--
-- Adding a column with a constant default is a metadata-only change in Postgres 11+
-- (no full table rewrite), so this plain ALTER is fast and takes only a brief lock.
ALTER TABLE llm_gateway_usage
    ADD COLUMN IF NOT EXISTS cost_usd double precision NOT NULL DEFAULT 0;

-- Backfill existing rows from the CURRENT catalog so history matches what the
-- dashboard showed under compute-on-read (no regression). Match the pricer's
-- normalization (exact, then -YYYYMMDD stripped, then single-digit dash→dot) and key
-- on model_name only — the pricer ignores provider. Cost is linear in tokens, so
-- per-row backfill sums to the same totals the per-model rollup produced.
--
-- A scalar subquery with ORDER BY CASE picks the SAME priority the pricer uses
-- (exact > date-stripped > dash→dot), so the result is deterministic even if a model
-- has both a dated and a normalized catalog row. The EXISTS guard leaves rows whose
-- model isn't priced at 0 (identical to what compute-on-read displayed).
UPDATE llm_gateway_usage u
SET cost_usd = (
    SELECT (
          u.input_tokens       * p.cost_per_million_input_tokens
        + u.output_tokens      * p.cost_per_million_output_tokens
        + u.cache_read_tokens  * p.cost_per_million_cached_input_tokens
        + u.cache_write_tokens * p.cost_per_million_cache_creation_tokens
      ) / 1000000.0
    FROM llm_model_pricing p
    WHERE p.model_name IN (
        u.model,
        regexp_replace(u.model, '-[0-9]{8}$', ''),
        regexp_replace(regexp_replace(u.model, '-[0-9]{8}$', ''), '([0-9])-([0-9])', '\1.\2')
    )
    ORDER BY CASE p.model_name
        WHEN u.model THEN 1
        WHEN regexp_replace(u.model, '-[0-9]{8}$', '') THEN 2
        ELSE 3
      END
    LIMIT 1
)
WHERE u.cost_usd = 0
  AND EXISTS (
      SELECT 1 FROM llm_model_pricing p
      WHERE p.model_name IN (
          u.model,
          regexp_replace(u.model, '-[0-9]{8}$', ''),
          regexp_replace(regexp_replace(u.model, '-[0-9]{8}$', ''), '([0-9])-([0-9])', '\1.\2')
      )
  );
