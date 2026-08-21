-- Restore UNIQUE (task_id, auto_pilot_id) on auto_pilot_task.
--
-- This fails if any auto optimize has meanwhile recorded the same resolution on
-- more than one task, which is exactly what the up migration allows.
-- Deduplicate before rolling back:
--
--   SELECT auto_pilot_id, task_id, count(*)
--   FROM public.auto_pilot_task
--   WHERE task_id IS NOT NULL
--   GROUP BY auto_pilot_id, task_id
--   HAVING count(*) > 1;

ALTER TABLE public.auto_pilot_task
    ADD CONSTRAINT scheduled_task_task_id_schedule_id_key UNIQUE (task_id, auto_pilot_id);
