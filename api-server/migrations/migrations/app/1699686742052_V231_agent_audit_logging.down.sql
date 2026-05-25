
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."agent_task" add column "source_id" uuid
--  null;


-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."agent_audit_log" add column "time_taken" integer
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."agent_audit_log" add column "method" text
--  null;

DROP TABLE "public"."agent_audit_log";

alter table "public"."agent_task" drop constraint "agent_task_agent_id_fkey";

DROP INDEX IF EXISTS "public"."agent_task_status_id_cloud_account_id_index_key";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."agent_task" add column "resoruce_id" uuid
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."agent_task" add column "source" text
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."agent_task" add column "agent_id" uuid
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."agent_task" add column "response" jsonb
--  null;
