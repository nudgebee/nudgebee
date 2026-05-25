
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."businessunit_users" add column "created_at" timestamp
--  not null default now();

alter table "public"."businessunit_users" alter column "created_at" set default now();
alter table "public"."businessunit_users" alter column "created_at" drop not null;
alter table "public"."businessunit_users" add column "created_at" time;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."business_unit" add column "created_at" timestamp
--  not null default now();

alter table "public"."business_unit" alter column "created_at" set default now();
alter table "public"."business_unit" alter column "created_at" drop not null;
alter table "public"."business_unit" add column "created_at" time;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."project_users" add column "created_at" timestamp
--  not null default now();

alter table "public"."project_users" alter column "created_at" set default now();
alter table "public"."project_users" alter column "created_at" drop not null;
alter table "public"."project_users" add column "created_at" time;
