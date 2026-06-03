
-- Revert last_sync_time column from timestamptz back to timestamp
ALTER TABLE "public"."knowledge_graph_tenant_filters"
  ALTER COLUMN "last_sync_time" TYPE timestamp USING last_sync_time::timestamp;
