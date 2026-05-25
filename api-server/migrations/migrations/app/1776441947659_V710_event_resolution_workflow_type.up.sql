-- Add WorkflowExecution to event_resolution type CHECK constraint.
-- This enables linking workflow executions back to the originating event
-- so the investigate page can show resolution status.

ALTER TABLE public.event_resolution DROP CONSTRAINT IF EXISTS type_check;
ALTER TABLE public.event_resolution ADD CONSTRAINT type_check
  CHECK (type = ANY (ARRAY['PullRequest'::text, 'Ticket'::text, 'DeploymentChange'::text, 'WorkflowExecution'::text]));
