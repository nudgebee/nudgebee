
alter table "public"."recommendation" add column "account_object_id" text
 null;

alter table "public"."recommendation" drop constraint "recommendation_cloud_account_id_rule_name_resource_id_category_";
alter table "public"."recommendation" add constraint "recommendation_category_rule_name_cloud_account_id_resource_id_account_object_id_key" unique ("category", "rule_name", "cloud_account_id", "resource_id", "account_object_id");

INSERT INTO "public"."recommendation_category_type"("value") VALUES (E'WarehouseQueryOptimization');
