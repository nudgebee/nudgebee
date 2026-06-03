
alter table "public"."cloud_resourses" alter column "k8s_namespace" drop not null;
alter table "public"."cloud_resourses" add column "k8s_namespace" text;

alter table "public"."cloud_resourses" alter column "k8s_node" drop not null;
alter table "public"."cloud_resourses" add column "k8s_node" text;

alter table "public"."cloud_resource_query_perf" rename column "bill_total_duration_micro" to "bill_total_secs";

alter table "public"."cloud_resource_query_perf" drop constraint "cloud_resource_query_perf_tenant_id_resource_id_account_id_query_id_database_name_db_username_key";

alter table "public"."cloud_resource_query_perf" rename column "query_queue_duration_micro" to "query_queue_duration_milli";

alter table "public"."cloud_resource_query_perf" rename column "query_planning_duration_micro" to "query_planning_duration_milli";

alter table "public"."cloud_resource_query_perf" rename column "query_exec_duration_micro" to "query_exec_duration_milli";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_query_perf" add column "query_queue_duration_milli" integer
--  null default '0';

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_query_perf" add column "query_id" text
--  not null;
