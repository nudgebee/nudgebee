
ALTER TABLE "public"."auto_playbook_executions" ALTER COLUMN "status" drop default;

DELETE FROM "public"."auto_playbook_execution_status" WHERE "values" = 'failed';

DELETE FROM "public"."auto_playbook_execution_status" WHERE "values" = 'complete';

DELETE FROM "public"."auto_playbook_execution_status" WHERE "values" = 'in_progress';

DROP TABLE "public"."auto_playbook_execution_status";

DROP TABLE "public"."auto_playbook_executions";
