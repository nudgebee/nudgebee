
INSERT INTO "public"."auto_playbook_execution_status"("description", "values") VALUES (E'execution of the task are in progress', E'IN_PROGRESS');

INSERT INTO "public"."auto_playbook_execution_status"("description", "values") VALUES (E'all the task are complete for this execution', E'COMPLETE');

INSERT INTO "public"."auto_playbook_execution_status"("description", "values") VALUES (E'all the task are failed for this execution', E'FAILED');

INSERT INTO "public"."auto_playbook_execution_status"("description", "values") VALUES (E'all task are executed for execution', E'EXECUTED');

INSERT INTO "public"."auto_playbook_execution_status"("description", "values") VALUES (E'some tasks are complete for execution', E'COMPLETE_WITH_ERROR');

INSERT INTO "public"."auto_playbook_execution_status"("description", "values") VALUES (E'the execution is scheduled for future', E'SCHEDULED');

INSERT INTO "public"."auto_playbook_execution_status"("description", "values") VALUES (E'the execution if skipped for execution by user', E'SKIPPED');

update
    auto_playbook_executions
set
    status = 'IN_PROGRESS'
where
    status = 'in_progress';

update
    auto_playbook_executions
set
    status = 'COMPLETE'
where
    status = 'complete';

update
    auto_playbook_executions
set
    status = 'FAILED'
where
    status = 'failed';

delete from
    auto_playbook_execution_status
where
    values in ('in_progress', 'complete', 'failed');
alter table "public"."auto_playbook_executions" alter column "tenant_id" set not null;

alter table "public"."auto_playbook_executions" alter column "account_id" set not null;

delete from
    auto_playbook_execution_status
where
    values in ('EXECUTED');
