
DELETE FROM "public"."recommendation_category_type" WHERE "value" = 'WarehouseQueryOptimization';

alter table "public"."recommendation" drop constraint "recommendation_category_rule_name_cloud_account_id_resource_id_account_object_id_key";
alter table "public"."recommendation" add constraint "recommendation_category_rule_name_cloud_account_id_resource_id_key" unique ("category", "rule_name", "cloud_account_id", "resource_id");

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."recommendation" add column "account_object_id" text
--  null;
