
INSERT INTO "public"."recommendation_category_type"("value") VALUES (E'WarehouseComputeOptimization') ON CONFLICT (value)  do nothing;
UPDATE "public"."recommendation_category_type" set value = 'WarehouseTableOptimization' where value = 'TableOptimization';

alter table "public"."cloud_resource_query_perf" add column "queue_provision_time" numeric
 null;

alter table "public"."cloud_resource_query_perf" add column "queue_repair_time" numeric
 null;

alter table "public"."cloud_resource_query_perf" add column "queue_overload_time" numeric
 null;

alter table "public"."cloud_resource_query_perf" add column "partitions_scanned" numeric
 null;

alter table "public"."cloud_resource_query_perf" add column "bytes_scanned" numeric
 null;

alter table "public"."cloud_resource_query_perf" add column "bytes_spilled_locally" numeric
 null;

alter table "public"."cloud_resource_query_perf" add column "bytes_spilled_remotely" numeric
 null;

alter table "public"."cloud_resource_query_perf" add column "transaction_block_time" numeric
 null;
