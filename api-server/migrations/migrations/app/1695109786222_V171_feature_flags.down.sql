
alter table "public"."feature_flag" drop constraint "feature_flag_feature_id_tenant_id_feature_module_id_key";

alter table "public"."feature_flag" alter column "feature_module_id" set not null;

alter table "public"."feature_flag" drop constraint "feature_flag_pkey";
alter table "public"."feature_flag"
    add constraint "feature_flag_pkey"
    primary key ("feature_id", "feature_module_id", "tenant_id");

alter table "public"."feature_flag" drop column "id" cascade
alter table "public"."feature_flag" drop column "id";
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE EXTENSION IF NOT EXISTS pgcrypto;


DELETE FROM "public"."feature" WHERE "value" = 'WEEKLY_SPEND_EMAIL_NOTIFICATION';

alter table "public"."feature_flag" rename column "feature_id" to "feature";

DROP TABLE "public"."feature_flag";

DROP TABLE "public"."feature";
