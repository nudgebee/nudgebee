
alter table "public"."auto_playbook"
  add constraint "auto_playbook_updated_by_fkey"
  foreign key ("updated_by")
  references "public"."users"
  ("id") on update restrict on delete restrict;
