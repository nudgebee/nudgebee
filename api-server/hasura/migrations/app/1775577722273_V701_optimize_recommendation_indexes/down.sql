-- Revert recommendation index optimization
DROP INDEX IF EXISTS idx_recommendation_account_status_rule_cat_updated;

CREATE INDEX idx_recommendation_finops_score
ON recommendation (finops_score DESC NULLS LAST) WHERE (status = 'Open');

CREATE INDEX idx_recommendation_jsonb_image
ON recommendation USING gin (recommendation);
