
alter table "public"."notification_rule_mappings" drop column "platform_installation_id" cascade;

alter table "public"."notification_rules" add column "aggregation_key" text
 null;

alter table "public"."notification_rules" add column "expires_at" timestamp
 null;
