
alter table "public"."auto_playbook_executions" add column "attribute" jsonb
 not null default jsonb_build_object();

update
    auto_playbook_executions
set
    attribute = '{"generic_notification":true,"schedule_notification":true}';
