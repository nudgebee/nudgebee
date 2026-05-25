
alter table "public"."recommendation" add column "rule_name" text
 null;

alter table "public"."recommendation" add constraint "recommendation_resource_id_rule_name_key" unique ("resource_id", "rule_name");
