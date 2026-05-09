-- Optimize recommendation table indexes for slow query patterns
--
-- Problem: Recommendation queries (92 slow query hits/day, avg 19s, max 63s) use
-- wrong indexes. The planner picks recommendation_rulename (just rule_name) to get
-- pre-sorted data for ROW_NUMBER() OVER (PARTITION BY rule_name, category, ...),
-- but scans ALL accounts' rows and filters by cloud_account_id post-scan.
--
-- Fix 1: Add composite index that serves both the WHERE clause (cloud_account_id, status)
-- AND provides sort order for the window function (rule_name, category, updated_at DESC).
CREATE INDEX idx_recommendation_account_status_rule_cat_updated
ON recommendation (cloud_account_id, status, rule_name, category, updated_at DESC);

-- Fix 2: Drop unused/wasteful indexes
-- idx_recommendation_finops_score: 0 scans since last stats reset, 3.8 MB
DROP INDEX IF EXISTS idx_recommendation_finops_score;

-- idx_recommendation_jsonb_image: only 21 scans but 768 MB (GIN on recommendation JSONB).
-- The same filtering is done more efficiently by idx_recommendation_security_scan_full
-- and idx_recommendation_security_image_by_account.
DROP INDEX IF EXISTS idx_recommendation_jsonb_image;
