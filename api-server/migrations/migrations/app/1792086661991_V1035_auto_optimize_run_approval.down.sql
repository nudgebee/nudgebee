-- The NOT NULL columns and the unique key are not restored: rows written by
-- per-run approvals would violate them.
DROP INDEX IF EXISTS public.auto_pilot_approvals_autopilot_id_status_idx;
