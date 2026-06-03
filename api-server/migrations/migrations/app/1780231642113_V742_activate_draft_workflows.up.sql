-- V742: Remove the DRAFT workflow status. DRAFT was an enablement state that
-- silently blocked all scheduled/event execution, which confused users who
-- published or saved a workflow and saw nothing run. The status is being
-- removed entirely; every existing DRAFT workflow is promoted to ACTIVE so its
-- triggers fire. Idempotent / safe to re-run.
--
-- NOTE: this only flips the status column. Temporal schedules for these rows
-- are (re)registered at runbook-server startup by Service.ReconcileSchedules
-- (create-if-missing, advisory-locked), since SQL cannot create schedules.
-- Webhook/event triggers self-heal because fan-out filters on status='ACTIVE'.

UPDATE workflows SET status = 'ACTIVE', updated_at = now() WHERE status = 'DRAFT';
