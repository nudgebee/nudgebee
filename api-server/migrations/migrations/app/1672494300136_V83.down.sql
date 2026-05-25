
alter table "public"."recommendation" drop constraint "recommendation_resource_id_rule_name_key";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."recommendation" add column "rule_name" text
--  null;
