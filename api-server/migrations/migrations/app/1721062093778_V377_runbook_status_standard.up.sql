
alter table "public"."auto_playbook_task" drop constraint "auto_playbook_task_status_fkey";


update auto_playbook_task_status set value = 'SCHEDULED' where value = 'Scheduled';
update auto_playbook_task_status set value = 'CREATED' where value = 'Created';
update auto_playbook_task_status set value = 'COMPLETE' where value = 'Complete';
update auto_playbook_task_status set value = 'QUEUED' where value = 'Queued';
update auto_playbook_task_status set value = 'FAILED' where value = 'Failed';
update auto_playbook_task_status set value = 'SKIPPED' where value = 'Skipped';
update auto_playbook_task_status set value = 'EXECUTED' where value = 'Executed';

update auto_playbook_task set status = 'EXECUTED' where status = 'Executed';
update auto_playbook_task set status = 'SCHEDULED' where status = 'Scheduled';
update auto_playbook_task set status = 'CREATED' where status = 'Created';
update auto_playbook_task set status = 'COMPLETE' where status = 'Complete';
update auto_playbook_task set status = 'QUEUED' where status = 'Queued';
update auto_playbook_task set status = 'FAILED' where status = 'Failed';
update auto_playbook_task set status = 'SKIPPED' where status = 'Skipped';

delete from auto_playbook_task_status where value = 'Dryrun';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM information_schema.table_constraints
        WHERE constraint_type = 'FOREIGN KEY'
        AND constraint_name = 'auto_playbook_task_status_fkey'
    ) THEN
        ALTER TABLE public.auto_playbook_task
        ADD CONSTRAINT auto_playbook_task_status_fkey
        FOREIGN KEY (status)
        REFERENCES public.auto_playbook_task_status (value)
        ON UPDATE RESTRICT
        ON DELETE RESTRICT;
    END IF;
END $$;


alter table "public"."auto_playbook" drop constraint "auto_playbook_status_fkey";

update public.auto_playbook_status set value = 'ACTIVE' where value = 'Active';
update public.auto_playbook_status set value = 'DISABLED' where value = 'Disabled';


update public.auto_playbook set status = 'ACTIVE' where status = 'Active';
update public.auto_playbook set status = 'DISABLED' where status = 'Disabled';
 

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM information_schema.table_constraints
        WHERE constraint_type = 'FOREIGN KEY'
        AND constraint_name = 'auto_playbook_status_fkey'
    ) THEN
        ALTER TABLE public.auto_playbook
        ADD CONSTRAINT auto_playbook_status_fkey
        FOREIGN KEY (status)
        REFERENCES public.auto_playbook_status (value)
        ON UPDATE RESTRICT
        ON DELETE RESTRICT;
    END IF;
END $$;
