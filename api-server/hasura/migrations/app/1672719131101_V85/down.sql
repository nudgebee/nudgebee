



-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."tickets" add column "url" text
--  null;

alter table "public"."tickets" drop constraint "tickets_severity_fkey";

alter table "public"."tickets" alter column "project_key" set default 'DEMO'::text;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."tickets" add column "severity" text
--  null;

DROP TABLE "public"."ticket_severity_type";
