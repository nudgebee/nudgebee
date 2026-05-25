
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."notification_rules" add column "expires_at" timestamp
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."notification_rules" add column "aggregation_key" text
--  null;

alter table "public"."notification_rule_mappings"
  add constraint "notification_rule_mappings_platform_installation_id_fkey"
  foreign key (platform_installation_id)
  references "public"."messaging_platforms"
  (id) on update cascade on delete cascade;
alter table "public"."notification_rule_mappings" alter column "platform_installation_id" drop not null;
alter table "public"."notification_rule_mappings" add column "platform_installation_id" uuid;
