
alter table "public"."project_fundings" drop constraint "project_fundings_businessunit_funding_project_key";

alter table "public"."project_fundings" drop constraint "project_fundings_businessunit_funding_fkey";

alter table "public"."project_fundings" drop constraint "project_fundings_updated_by_fkey";

alter table "public"."project_fundings" drop constraint "project_fundings_created_by_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."project_fundings" add column "end_date" timestamp
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."project_fundings" add column "start_date" timestamp
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."project_fundings" add column "updated_by" uuid
--  not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."project_fundings" add column "created_by" uuid
--  not null;

alter table "public"."project_fundings" alter column "created_at" drop not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."project_fundings" add column "updated_at" timestamp
--  not null default now();

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."project_fundings" add column "created_at" timestamp
--  null default now();

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."project_fundings" add column "planned_amount" float8
--  null default '0.0';

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."project_fundings" add column "amount" float8
--  null default '0.0';

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."project_fundings" add column "businessunit_funding" uuid
--  not null;

alter table "public"."project_fundings"
  add constraint "project_fundings_funding_source_fkey"
  foreign key (funding_source)
  references "public"."funding_sources"
  (id) on update restrict on delete restrict;
alter table "public"."project_fundings" alter column "funding_source" drop not null;
alter table "public"."project_fundings" add column "funding_source" uuid;
