
alter table "public"."compliance_standard" add column "owner" uuid
 null;

alter table "public"."compliance_standard"
  add constraint "compliance_standard_owner_fkey"
  foreign key ("owner")
  references "public"."users"
  ("id") on update restrict on delete restrict;

ALTER TABLE "public"."compliance_standard" ALTER COLUMN "name" TYPE citext;
