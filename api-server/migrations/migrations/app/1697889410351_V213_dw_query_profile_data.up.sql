

CREATE TABLE "public"."dw_query_profile_data" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "query_id" text, "step_id" numeric, "operator_id" numeric, "parent_operators" text, "operator_type" text, "operator_statistics" text, "execution_time_breakdown" text, "operator_attributes" text, PRIMARY KEY ("id") );
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."dw_query_profile_data" add column "cloud_account_id" text
 not null;

alter table "public"."dw_query_profile_data" add column "tenant_id" uuid
 not null;

alter table "public"."dw_query_profile_data" add column "query_group_md5" text
 null;

alter table "public"."dw_query_profile_data" add column "profile_data" jsonb
 null;

alter table "public"."dw_query_profile_data" add column "db_type" text
 not null;

alter table "public"."dw_query_profile_data" drop column "step_id" cascade;

alter table "public"."dw_query_profile_data" drop column "parent_operators" cascade;

alter table "public"."dw_query_profile_data" drop column "operator_id" cascade;

alter table "public"."dw_query_profile_data" drop column "operator_type" cascade;

alter table "public"."dw_query_profile_data" drop column "operator_statistics" cascade;

alter table "public"."dw_query_profile_data" drop column "execution_time_breakdown" cascade;

alter table "public"."dw_query_profile_data" drop column "operator_attributes" cascade;

alter table "public"."dw_query_profile_data"
  add constraint "dw_query_profile_data_db_type_fkey"
  foreign key ("db_type")
  references "public"."db_type"
  ("value") on update restrict on delete restrict;
