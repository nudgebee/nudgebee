-- Stop workflows.updated_at from auto-bumping on every row write.
--
-- The set_public_workflows_updated_at trigger (added in V597) fired on every
-- UPDATE to workflows, so execution-status writes, status toggles and
-- version-pointer writes all bumped updated_at. That made the listing's
-- "Updated <time>" reflect last-touched rather than last-edited, disagreeing
-- with updated_by (last-edited). updated_at is now set explicitly only on the
-- genuine user-edit path (WorkflowDao.Update). Drop the trigger only; the shared
-- set_current_timestamp_updated_at() function stays — other tables use it.
DROP TRIGGER IF EXISTS set_public_workflows_updated_at ON public.workflows;
