
alter table "public"."notification_rule_mappings" drop constraint "notification_rule_mappings_platform_installation_id_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."notification_rule_mappings" add column "platform_installation_id" uuid
--  null;
