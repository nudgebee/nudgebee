
alter table "public"."auto_playbook_task" add column "execution_id" uuid
 null;

alter table "public"."auto_playbook_task"
  add constraint "auto_playbook_task_execution_id_fkey"
  foreign key ("execution_id")
  references "public"."auto_playbook_executions"
  ("id") on update restrict on delete restrict;
