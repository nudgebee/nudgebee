
alter table "public"."recommendation" add column "updated_by" uuid
 null;

alter table "public"."recommendation"
  add constraint "recommendation_updated_by_fkey"
  foreign key ("updated_by")
  references "public"."users"
  ("id") on update restrict on delete restrict;
