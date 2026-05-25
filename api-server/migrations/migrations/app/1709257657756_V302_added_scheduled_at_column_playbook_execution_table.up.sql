

alter table "public"."auto_playbook_executions" add column "scheduled_at" timestamp
 not null default now();

update
    auto_playbook_executions
set
    scheduled_at = created_at;
