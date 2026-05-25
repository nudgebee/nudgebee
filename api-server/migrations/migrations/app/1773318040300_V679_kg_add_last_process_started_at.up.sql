ALTER TABLE "public"."knowledge_graph_tenant_filters"
  ADD COLUMN IF NOT EXISTS "last_process_started_at" timestamp NULL;

ALTER TABLE "public"."knowledge_graph_tenant_filters"
  ALTER COLUMN "created_at" TYPE timestamp USING created_at AT TIME ZONE 'UTC';

ALTER TABLE "public"."knowledge_graph_tenant_filters"
  ALTER COLUMN "last_sync_time" TYPE timestamp USING last_sync_time AT TIME ZONE 'UTC';
