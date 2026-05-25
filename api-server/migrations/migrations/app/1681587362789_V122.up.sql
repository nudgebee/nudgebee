
alter table "public"."recommendation" add column "account_id" uuid
 null;

alter table "public"."recommendation" drop column "account_id" cascade;

alter table "public"."recommendation" drop constraint "recommendation_resource_id_rule_name_key";
alter table "public"."recommendation" add constraint "recommendation_rule_name_resource_id_cloud_account_id_key" unique ("rule_name", "resource_id", "cloud_account_id");
