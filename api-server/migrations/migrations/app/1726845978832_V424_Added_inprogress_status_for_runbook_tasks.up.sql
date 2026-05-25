INSERT INTO
    "public"."auto_playbook_task_status"("description", "value")
VALUES
    ('the task is in in progress', 'IN_PROGRESS');

update
    auto_playbook_task
set
    status = 'IN_PROGRESS'
where
    status = 'EXECUTED';