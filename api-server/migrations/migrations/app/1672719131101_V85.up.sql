



CREATE TABLE "public"."ticket_severity_type" ("value" text NOT NULL, "comment" text NOT NULL, PRIMARY KEY ("value") );

alter table "public"."tickets" add column "severity" text
 null;

alter table "public"."tickets" alter column "project_key" set default 'DESK'::text;

alter table "public"."tickets"
  add constraint "tickets_severity_fkey"
  foreign key ("severity")
  references "public"."ticket_severity_type"
  ("value") on update no action on delete no action;

alter table "public"."tickets" add column "url" text
 null;
