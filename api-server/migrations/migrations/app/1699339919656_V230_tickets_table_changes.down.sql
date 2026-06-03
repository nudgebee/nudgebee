
alter table "public"."tickets"
  add constraint "tickets_severity_fkey"
  foreign key ("severity")
  references "public"."ticket_severity_type"
  ("value") on update no action on delete no action;

alter table "public"."tickets" alter column "title" drop not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."tickets" add column "type" text
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."tickets" add column "description" text
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."tickets" add column "title" text
--  null;
