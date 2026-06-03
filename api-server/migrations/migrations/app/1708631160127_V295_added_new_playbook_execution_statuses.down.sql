
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- delete from
--     auto_playbook_execution_status
-- where
--     values in ('EXECUTED');

alter table "public"."auto_playbook_executions" alter column "account_id" drop not null;

alter table "public"."auto_playbook_executions" alter column "tenant_id" drop not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- delete from
--     auto_playbook_execution_status
-- where
--     values in ('in_progress', 'complete', 'failed');
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- update
--     auto_playbook_executions
-- set
--     status = 'IN_PROGRESS'
-- where
--     status = 'in_progress';
--
-- update
--     auto_playbook_executions
-- set
--     status = 'COMPLETE'
-- where
--     status = 'complete';
--
-- update
--     auto_playbook_executions
-- set
--     status = 'FAILED'
-- where
--     status = 'failed';

DELETE FROM "public"."auto_playbook_execution_status" WHERE "values" = 'SKIPPED';

DELETE FROM "public"."auto_playbook_execution_status" WHERE "values" = 'SCHEDULED';

DELETE FROM "public"."auto_playbook_execution_status" WHERE "values" = 'COMPLETE_WITH_ERROR';

DELETE FROM "public"."auto_playbook_execution_status" WHERE "values" = 'EXECUTED';

DELETE FROM "public"."auto_playbook_execution_status" WHERE "values" = 'FAILED';

DELETE FROM "public"."auto_playbook_execution_status" WHERE "values" = 'COMPLETE';

DELETE FROM "public"."auto_playbook_execution_status" WHERE "values" = 'IN_PROGRESS';
