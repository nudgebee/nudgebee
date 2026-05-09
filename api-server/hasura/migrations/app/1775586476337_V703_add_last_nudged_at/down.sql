DROP INDEX IF EXISTS idx_recommendation_last_nudged_at;
ALTER TABLE recommendation DROP COLUMN IF EXISTS last_nudged_at;
