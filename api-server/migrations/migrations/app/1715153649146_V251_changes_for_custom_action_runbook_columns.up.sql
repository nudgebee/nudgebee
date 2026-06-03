
alter table "public"."runbook_custom_action" add column "description" text
 null;

alter table "public"."runbook_custom_action" alter column "created_by" drop not null;
