
alter table "public"."knowledge_graph_metadata" add column "additional_properties" jsonb
 null;

alter table "public"."knowledge_graph_metadata" add column "last_sync_at" timestamp
 null;
