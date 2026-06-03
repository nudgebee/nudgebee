
alter table "public"."recommendation" drop constraint "recommendation_rule_name_resource_id_cloud_account_id_key";
alter table "public"."recommendation" add constraint "recommendation_rule_name_resource_id_key" unique ("rule_name", "resource_id");

alter table "public"."recommendation" alter column "account_id" drop not null;
alter table "public"."recommendation" add column "account_id" uuid;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."recommendation" add column "account_id" uuid
--  null;
