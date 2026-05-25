
alter table "public"."tickets" add column "reporter" text
 null;

alter table "public"."tickets" add column "tags" text
 null;

alter table "public"."tickets" add column "platform" text
 not null default 'JIRA';

ALTER TABLE "public"."tickets" ALTER COLUMN "platform" drop default;

CREATE TABLE "public"."ticket_platforms" ("value" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."ticket_platforms"("value") VALUES (E'JIRA');

alter table "public"."tickets"
  add constraint "tickets_platform_fkey"
  foreign key ("platform")
  references "public"."ticket_platforms"
  ("value") on update no action on delete no action;
