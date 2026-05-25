
alter table "public"."auto_playbook_executions"
  add constraint "auto_playbook_executions_tenant_id_fkey"
  foreign key ("tenant_id")
  references "public"."tenant"
  ("id") on update restrict on delete restrict;

alter table "public"."auto_playbook_executions"
  add constraint "auto_playbook_executions_account_id_fkey"
  foreign key ("account_id")
  references "public"."cloud_accounts"
  ("id") on update restrict on delete restrict;
