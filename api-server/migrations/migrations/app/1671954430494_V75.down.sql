
ALTER TABLE "public"."tickets" ALTER COLUMN "project_key" drop default;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."tickets" add column "project_key" text
--  not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."jira_configurations" add column "projects" json
--  null;
