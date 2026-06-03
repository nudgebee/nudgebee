
-- Fix the data type of last_sync_time column from timestamp to timestamptz
-- This ensures it matches the GraphQL schema definition (timestamptz)
ALTER TABLE "public"."knowledge_graph_tenant_filters"
  ALTER COLUMN "last_sync_time" TYPE timestamptz USING last_sync_time AT TIME ZONE 'UTC';
