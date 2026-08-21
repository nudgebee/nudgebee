-- Drop UNIQUE (task_id, auto_pilot_id) on auto_pilot_task.
--
-- The constraint is inherited from the original scheduled_task table (V232),
-- which predates GitOps pull requests. auto_pilot_task.task_id now holds the
-- recommendation_resolution id, and since #33523 an auto optimize run that finds
-- an already-open pull request is deliberately handed back the existing
-- resolution rather than opening a second one. Recording the same resolution on
-- a later run is therefore the intended outcome, not a duplicate.
--
-- With the constraint in place that save failed with a duplicate key before the
-- run could write any status, leaving the task in Scheduled. A task stuck in
-- Scheduled is indistinguishable from one waiting its turn, so it held its
-- recommendation back from every subsequent run and silently disabled that
-- workload permanently (#34943).
--
-- Nothing reads task_id as a uniqueness key: the pull-request follow-up paths
-- join on recommendation_id, and the UI only displays the column.

ALTER TABLE public.auto_pilot_task
    DROP CONSTRAINT IF EXISTS scheduled_task_task_id_schedule_id_key;
