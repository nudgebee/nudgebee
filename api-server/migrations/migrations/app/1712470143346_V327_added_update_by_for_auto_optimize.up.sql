
alter table "public"."auto_pilot" add column "update_by" uuid
 null;

alter table "public"."auto_pilot"
  add constraint "auto_pilot_update_by_fkey"
  foreign key ("update_by")
  references "public"."users"
  ("id") on update restrict on delete restrict;
