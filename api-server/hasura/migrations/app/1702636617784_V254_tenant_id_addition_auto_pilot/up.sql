
alter table "public"."auto_pilot" add column "tenant_id" uuid
 not null;

alter table "public"."auto_pilot"
  add constraint "auto_pilot_tenant_id_fkey"
  foreign key ("tenant_id")
  references "public"."tenant"
  ("id") on update restrict on delete restrict;

alter table "public"."auto_pilot_task" add column "tenant_id" uuid
 not null;

alter table "public"."auto_pilot_task"
  add constraint "auto_pilot_task_tenant_id_fkey"
  foreign key ("tenant_id")
  references "public"."tenant"
  ("id") on update restrict on delete restrict;
