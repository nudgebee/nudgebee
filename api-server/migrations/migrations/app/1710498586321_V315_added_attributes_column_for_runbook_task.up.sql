
alter table "public"."auto_playbook_task" add column "attribute" jsonb
 not null default jsonb_build_object();

alter table "public"."auto_playbook_task" rename column "attribute" to "attributes";
