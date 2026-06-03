INSERT INTO
    "public"."auto_pilot_task_status"("description", "value")
VALUES
    ('the task is in progress', 'In_Progress');

update
    auto_pilot_task
set
    status = 'In_Progress'
where
    status = 'Executed';