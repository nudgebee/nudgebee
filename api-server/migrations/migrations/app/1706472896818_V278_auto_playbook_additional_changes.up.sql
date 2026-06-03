
alter table "public"."auto_playbook_task" add column "account_id" uuid
 not null;

alter table "public"."auto_playbook_task"
  add constraint "auto_playbook_task_account_id_fkey"
  foreign key ("account_id")
  references "public"."cloud_accounts"
  ("id") on update restrict on delete restrict;

alter table "public"."auto_playbook_task"
  add constraint "auto_playbook_task_auto_playbook_id_fkey2"
  foreign key ("auto_playbook_id")
  references "public"."auto_playbook"
  ("id") on update restrict on delete restrict;

alter table "public"."auto_playbook_task" add column "resource_id" uuid
 not null;

alter table "public"."auto_playbook" rename column "source" to "trigger";

alter table "public"."auto_playbook_task" add column "task_type" text
 not null;
