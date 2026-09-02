-- Reconcile recommendation rows whose user-set dismissal was silently reverted
-- by a re-scan. The scanner-persist path archived a Dismissed row and then
-- reopened it via the upsert, leaving is_dismissed = true stranded behind a
-- status the read path treats as Open (no query filters on is_dismissed). The
-- code fix stops NEW reversions; this converges the historical rows so the
-- invariant  is_dismissed = true  <=>  status = 'Dismissed'  holds again. It
-- also removes the Archive + is_dismissed = true rows a later re-scan would
-- otherwise re-mint into the forbidden Open + is_dismissed = true state.
--
-- Idempotent: once a row is Dismissed (or its flag cleared) it no longer matches
-- either predicate, so a re-run is a no-op.

-- Re-honour active dismissals and snoozes: restore status = 'Dismissed'. A row
-- snoozed into the future stays snoozed; the expiry sweep returns it to Open
-- when the timestamp passes.
UPDATE recommendation
SET status = 'Dismissed'
WHERE is_dismissed = true
  AND status IN ('Open', 'Archive')
  AND (snoozed_until IS NULL OR snoozed_until > NOW());

-- Snoozes whose window already lapsed: clear the dismissal fields so the row is
-- a clean, un-dismissed finding. Status is left as-is — an Open row stays
-- visible, a resolved finding stays Archive — matching the snooze-expiry sweep.
UPDATE recommendation
SET is_dismissed = false, dismissed_reason = NULL, snoozed_until = NULL
WHERE is_dismissed = true
  AND status IN ('Open', 'Archive')
  AND snoozed_until IS NOT NULL
  AND snoozed_until <= NOW();
