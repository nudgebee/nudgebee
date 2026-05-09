
alter table "public"."funding_sources" alter column "amount" drop not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."funding_sources" add column "amount" float8
--  null;

alter table "public"."funding_sources" alter column "amount" drop not null;
alter table "public"."funding_sources" add column "amount" money;
