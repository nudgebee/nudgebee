
alter table "public"."cloud_resource_query_perf" alter column "database_name" drop not null;

alter table "public"."cloud_resource_query_perf" alter column "id" set default gen_random_uuid();

alter table "public"."cloud_resource_query_perf" alter column "created_at" set default now();

alter table "public"."cloud_resource_query_perf" alter column "rpu" set default '0.0';
