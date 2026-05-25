

alter table "public"."events" drop constraint "events_urgency_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."events" add column "urgency" text
--  not null default 'low';
