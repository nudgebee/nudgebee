
alter table "public"."cloud_resourses" alter column "platform" set not null;

alter table "public"."cloud_resourses" alter column "business_unit" drop not null;
alter table "public"."cloud_resourses" add column "business_unit" uuid;

alter table "public"."cloud_resourses"
  add constraint "cloud_resourses_business_unit_fkey"
  foreign key ("business_unit")
  references "public"."business_unit"
  ("id") on update cascade on delete cascade;
