-- Revert cloud_resourses and cloud_resource_metrics index optimization
DROP INDEX IF EXISTS idx_cloud_resource_metrics_resource_ts;
DROP INDEX IF EXISTS idx_cloud_resourses_account_status;

CREATE INDEX cloud_resource_metrics_tags
ON cloud_resource_metrics USING gin (tags);

CREATE INDEX cloud_resource_metrics_tenant
ON cloud_resource_metrics (tenant_id);
