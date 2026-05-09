
alter table "public"."businessunit_funding" rename to "businessunit_funding_sources";

alter table "public"."businessunit_funding_sources" alter column "start_date" set not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."businessunit_funding_sources" add column "end_date" timestamp
--  null;

alter table "public"."businessunit_funding_sources" alter column "end_date" drop not null;
alter table "public"."businessunit_funding_sources" add column "end_date" time;

alter table "public"."funding_sources"
  add constraint "funding_sources_business_unit_fkey"
  foreign key (business_unit)
  references "public"."business_unit"
  (id) on update cascade on delete cascade;
alter table "public"."funding_sources" alter column "business_unit" drop not null;
alter table "public"."funding_sources" add column "business_unit" uuid;

DROP TABLE "public"."businessunit_funding_sources";
