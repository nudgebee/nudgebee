-- #40734: a remediation run is recorded before it is sent, and each plan step is decided once.
-- Unknown: may have run, outcome unknown, never auto-retried. Declined: a person declined a step
-- (written from #40734's next change; added here so one migration touches this CHECK).
-- Lock window: ~281 rows prod / 527 test (2026-10-14). The CHECK re-add (ACCESS EXCLUSIVE) and index build
-- (SHARE) each take well under 1 s; lock_timeout fails the migration rather than queue behind a long tx.
SET LOCAL lock_timeout = '5s';

ALTER TABLE public.event_resolution DROP CONSTRAINT IF EXISTS status_check;
ALTER TABLE public.event_resolution ADD CONSTRAINT status_check
  CHECK (status = ANY (ARRAY['InProgress'::text, 'Failed'::text, 'Success'::text, 'Configuring'::text, 'Unknown'::text, 'Declined'::text]));

-- One decision per plan step: data.approval_key hashes account, event, plan version, step & command
-- (llm-server events/remediation_decisions.go). Unknown and not_sent runs stay out: both may run again.
CREATE UNIQUE INDEX IF NOT EXISTS uq_event_resolution_approval_key
    ON public.event_resolution ((data->>'approval_key'))
 WHERE type = 'CommandExecution'
   AND data->>'approval_key' IS NOT NULL
   AND status <> 'Unknown'
   AND COALESCE(data->>'not_sent', '') <> 'true';
