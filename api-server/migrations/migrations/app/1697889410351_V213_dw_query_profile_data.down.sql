
alter table "public"."dw_query_profile_data" drop constraint "dw_query_profile_data_db_type_fkey";

alter table "public"."dw_query_profile_data" alter column "operator_attributes" drop not null;
alter table "public"."dw_query_profile_data" add column "operator_attributes" text;

alter table "public"."dw_query_profile_data" alter column "execution_time_breakdown" drop not null;
alter table "public"."dw_query_profile_data" add column "execution_time_breakdown" text;

alter table "public"."dw_query_profile_data" alter column "operator_statistics" drop not null;
alter table "public"."dw_query_profile_data" add column "operator_statistics" text;

alter table "public"."dw_query_profile_data" alter column "operator_type" drop not null;
alter table "public"."dw_query_profile_data" add column "operator_type" text;

alter table "public"."dw_query_profile_data" alter column "operator_id" drop not null;
alter table "public"."dw_query_profile_data" add column "operator_id" numeric;

alter table "public"."dw_query_profile_data" alter column "parent_operators" drop not null;
alter table "public"."dw_query_profile_data" add column "parent_operators" text;

alter table "public"."dw_query_profile_data" alter column "step_id" drop not null;
alter table "public"."dw_query_profile_data" add column "step_id" numeric;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."dw_query_profile_data" add column "db_type" text
--  not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."dw_query_profile_data" add column "profile_data" jsonb
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."dw_query_profile_data" add column "query_group_md5" text
--  null;


-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."dw_query_profile_data" add column "tenant_id" uuid
--  not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."dw_query_profile_data" add column "cloud_account_id" text
--  not null;

DROP TABLE "public"."dw_query_profile_data";
