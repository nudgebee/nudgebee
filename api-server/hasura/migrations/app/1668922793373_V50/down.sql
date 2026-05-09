
alter table "public"."projects" drop constraint "projects_approved_by_fkey";

alter table "public"."projects" drop constraint "projects_it_manager_fkey";

alter table "public"."projects" drop constraint "projects_project_manager_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."projects" add column "expected_revenue" float8
--  null default '0.0';

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."projects" add column "it_manager" uuid
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."projects" add column "project_manager" uuid
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."projects" add column "approved_by" uuid
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."projects" add column "billable" boolean
--  null default 'false';
