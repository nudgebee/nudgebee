
alter table "public"."agent" rename column "access_secret_v2" to "agent_secret_v2";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."agent" add column "agent_secret_v2" text
--  null;
