-- Optimize cloud_resourses and cloud_resource_metrics indexes
--
-- Problem 1: cloud_resources_list_v2 query (12 hits/day, avg 28s, max 47s)
-- uses LATERAL JOIN to get latest metric per resource. The current index
-- (cloud_resource_id) requires a bitmap scan + sort per resource (2,684 loops).
--
-- Problem 2: cloud_resourses scans 79K rows to find 2.7K Active resources (94% filtered).
-- Index (tenant, account) exists but doesn't include status.

-- Fix 1: Add (cloud_resource_id, timestamp DESC) index for LATERAL JOIN.
-- Turns bitmap scan + sort into a single index-only lookup per resource.
CREATE INDEX idx_cloud_resource_metrics_resource_ts
ON cloud_resource_metrics (cloud_resource_id, "timestamp" DESC);

-- Fix 2: Add (account, status) index for cloud_resourses.
-- Avoids scanning 74K+ Inactive rows per account.
CREATE INDEX idx_cloud_resourses_account_status
ON cloud_resourses (account, status);

-- Fix 3: Drop unused/barely-used indexes on cloud_resource_metrics
-- cloud_resource_metrics_tags: 1 scan, 120 MB (GIN)
DROP INDEX IF EXISTS cloud_resource_metrics_tags;
-- cloud_resource_metrics_tenant: 1 scan since last stats reset, 27 MB.
-- Queries on cloud_resource_metrics filter by cloud_resource_id (via LATERAL JOIN),
-- not by tenant_id directly. The new idx_cloud_resource_metrics_resource_ts covers
-- the actual query pattern.
DROP INDEX IF EXISTS cloud_resource_metrics_tenant;
