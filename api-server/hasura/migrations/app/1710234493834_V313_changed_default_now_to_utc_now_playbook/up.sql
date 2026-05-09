
alter table "public"."auto_playbook_task" alter column "created_at" set default (timezone('utc', now()));

alter table "public"."auto_playbook_task" alter column "updated_at" set default (timezone('utc', now()));

alter table "public"."auto_playbook_task" alter column "scheduled_time" set default (timezone('utc', now()));

alter table "public"."auto_playbook_executions" alter column "created_at" set default (timezone('utc', now()));

alter table "public"."auto_playbook_executions" alter column "scheduled_at" set default (timezone('utc', now()));

alter table "public"."auto_playbook" alter column "created_at" set default (timezone('utc', now()));

alter table "public"."auto_playbook" alter column "update_at" set default (timezone('utc', now()));

alter table "public"."auto_playbook" alter column "start_at" set default (timezone('utc', now()));
