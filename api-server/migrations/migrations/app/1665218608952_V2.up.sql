
alter table "public"."business_unit" add column "parent_business_unit" uuid
 null;

alter table "public"."business_unit"
  add constraint "business_unit_parent_business_unit_fkey"
  foreign key ("parent_business_unit")
  references "public"."business_unit"
  ("id") on update restrict on delete restrict;
