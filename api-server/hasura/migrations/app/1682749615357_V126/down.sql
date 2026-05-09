
alter table "public"."recommendation" drop constraint "recommendation_cloud_account_id_rule_name_resource_id_category_key";
alter table "public"."recommendation" add constraint "recommendation_cloud_account_id_rule_name_resource_id_key" unique ("cloud_account_id", "rule_name", "resource_id");
