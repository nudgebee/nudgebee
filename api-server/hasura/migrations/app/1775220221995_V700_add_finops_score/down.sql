DROP INDEX IF EXISTS idx_recommendation_finops_band;
DROP INDEX IF EXISTS idx_recommendation_finops_score;

ALTER TABLE recommendation
  DROP COLUMN IF EXISTS finops_score_breakdown,
  DROP COLUMN IF EXISTS finops_band,
  DROP COLUMN IF EXISTS finops_score;
