CREATE INDEX IF NOT EXISTS idx_recommendation_resource_account
ON recommendation (resource_id, cloud_account_id)
INCLUDE (estimated_savings, rule_name, category, status, severity);
