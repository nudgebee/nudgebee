-- Add CommandExecution to the event_resolution type CHECK constraint.
-- This lets the remediation panel record a successful command run against an event
-- so the investigate page can show "already applied" on reload.

ALTER TABLE public.event_resolution DROP CONSTRAINT IF EXISTS type_check;
ALTER TABLE public.event_resolution ADD CONSTRAINT type_check
  CHECK (type = ANY (ARRAY['PullRequest'::text, 'Ticket'::text, 'DeploymentChange'::text, 'WorkflowExecution'::text, 'CommandExecution'::text]));
