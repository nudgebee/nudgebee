-- Revert: remove CommandExecution from the event_resolution type CHECK constraint.

ALTER TABLE public.event_resolution DROP CONSTRAINT IF EXISTS type_check;
ALTER TABLE public.event_resolution ADD CONSTRAINT type_check
  CHECK (type = ANY (ARRAY['PullRequest'::text, 'Ticket'::text, 'DeploymentChange'::text, 'WorkflowExecution'::text]));
