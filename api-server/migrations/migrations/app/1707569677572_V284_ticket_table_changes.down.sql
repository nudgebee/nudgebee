
alter table "public"."tickets" alter column "created_by" set not null;

alter table "public"."tickets"
  add constraint "tickets_created_by_fkey"
  foreign key ("created_by")
  references "public"."users"
  ("id") on update no action on delete no action;
