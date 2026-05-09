-- Composite index to fix K8sOptimizeSummaryInfographics query timeout (~24s → <500ms)
-- The query engine always injects tenant_id from security context, then filters by cloud_account_id + status (± category, rule_name)
-- Without this index, the count_recommendations query does a full seq scan on the 2.8GB recommendation table
CREATE INDEX IF NOT EXISTS idx_recommendation_tenant_account_status
ON recommendation (tenant_id, cloud_account_id, status, category, rule_name)
INCLUDE (estimated_savings);
