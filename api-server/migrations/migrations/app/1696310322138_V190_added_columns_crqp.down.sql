
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_query_perf" add column "transaction_block_time" numeric
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_query_perf" add column "bytes_spilled_remotely" numeric
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_query_perf" add column "bytes_spilled_locally" numeric
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_query_perf" add column "bytes_scanned" numeric
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_query_perf" add column "partitions_scanned" numeric
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_query_perf" add column "queue_overload_time" numeric
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_query_perf" add column "queue_repair_time" numeric
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_query_perf" add column "queue_provision_time" numeric
--  null;

DELETE FROM "public"."recommendation_category_type" WHERE "value" = 'WarehouseComputeOptimization';
