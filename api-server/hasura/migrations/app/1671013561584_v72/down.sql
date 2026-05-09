
ALTER TABLE "public"."tickets" ALTER COLUMN "assignee" TYPE uuid;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."tickets" add column "assignee" uuid
--  null;


DROP TABLE "public"."tickets";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."jira_configurations" add column "last_connected" timestamp
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."jira_configurations" add column "status" text
--  null;

DROP TABLE "public"."jira_configurations";
