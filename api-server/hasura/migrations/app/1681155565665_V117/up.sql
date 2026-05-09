
alter table "public"."cloud_resource_query_perf" add column "query_id" text
 not null;

alter table "public"."cloud_resource_query_perf" add column "query_queue_duration_milli" integer
 null default '0';

alter table "public"."cloud_resource_query_perf" rename column "query_exec_duration_milli" to "query_exec_duration_micro";

alter table "public"."cloud_resource_query_perf" rename column "query_planning_duration_milli" to "query_planning_duration_micro";

alter table "public"."cloud_resource_query_perf" rename column "query_queue_duration_milli" to "query_queue_duration_micro";

alter table "public"."cloud_resource_query_perf" add constraint "cloud_resource_query_perf_tenant_id_resource_id_account_id_query_id_database_name_db_username_key" unique ("tenant_id", "resource_id", "account_id", "query_id", "database_name", "db_username");

alter table "public"."cloud_resource_query_perf" rename column "bill_total_secs" to "bill_total_duration_micro";

alter table "public"."cloud_resourses" drop column "k8s_node" cascade;

alter table "public"."cloud_resourses" drop column "k8s_namespace" cascade;
