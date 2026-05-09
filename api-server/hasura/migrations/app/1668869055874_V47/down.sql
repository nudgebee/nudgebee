
alter table "public"."project_cloud_resources" drop constraint "allocation_pct_check";
alter table "public"."project_cloud_resources" add constraint "allocation_pct_check" check (CHECK (allocation_pct < 100::double precision));

alter table "public"."project_cloud_resources" drop constraint "allocation_end_check";

alter table "public"."project_cloud_resources" drop constraint "allocation_pct_check";

alter table "public"."project_cloud_resources" drop constraint "project_cloud_resources_updated_by_fkey";

alter table "public"."project_cloud_resources" drop constraint "project_cloud_resources_created_by_fkey";

alter table "public"."project_accounts" drop constraint "project_accounts_updated_by_fkey";

alter table "public"."project_accounts" drop constraint "project_accounts_created_by_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."project_accounts" add column "updated_by" uuid
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."project_accounts" add column "created_by" uuid
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."project_accounts" add column "updated_at" timestamp
--  null default now();

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."project_accounts" add column "created_at" timestamp
--  null default now();

alter table "public"."project_accounts" drop constraint "allocaion_end_check";

alter table "public"."project_accounts" drop constraint "project_accounts_project_id_account_id_key";

alter table "public"."project_accounts" drop constraint "allocation_pct_check";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."project_accounts" add column "allocation_pct" double precision
--  null default '100';

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."project_accounts" add column "allocation_end" timestamp
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."project_accounts" add column "allocation_start" timestamp
--  null default now();
