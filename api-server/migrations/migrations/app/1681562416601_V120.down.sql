
ALTER TABLE "public"."cloud_resource_query_perf" ALTER COLUMN "rpu" drop default;

ALTER TABLE "public"."cloud_resource_query_perf" ALTER COLUMN "created_at" drop default;

ALTER TABLE "public"."cloud_resource_query_perf" ALTER COLUMN "id" drop default;

alter table "public"."cloud_resource_query_perf" alter column "database_name" set not null;
