
alter table "public"."auto_playbook_task"
  add constraint "auto_playbook_task_auto_playbook_id_fkey2"
  foreign key ("auto_playbook_id")
  references "public"."auto_playbook"
  ("id") on update restrict on delete restrict;
