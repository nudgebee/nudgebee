
alter table "public"."tickets" drop constraint "tickets_platform_fkey";

DELETE FROM "public"."ticket_platforms" WHERE "value" = 'JIRA';

DROP TABLE "public"."ticket_platforms";

alter table "public"."tickets" alter column "platform" set default 'JIRA'::text;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."tickets" add column "platform" text
--  not null default 'JIRA';

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."tickets" add column "tags" text
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."tickets" add column "reporter" text
--  null;
