
UPDATE
    auto_playbook_task
SET
    scheduled_time = t2.scheduled_at
FROM
    auto_playbook_executions t2
WHERE
    execution_id = t2.id;
