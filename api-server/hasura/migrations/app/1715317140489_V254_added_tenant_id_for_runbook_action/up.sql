
alter table "public"."runbook_action" add column "tenant_id" uuid
 null;

alter table "public"."runbook_action_library" add column "tenant_id" uuid
 null;
