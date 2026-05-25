
alter table "public"."auto_playbook_task" drop column "resource_id" cascade;

alter table "public"."auto_playbook_task" add column "resource" jsonb
 null default jsonb_build_object();
