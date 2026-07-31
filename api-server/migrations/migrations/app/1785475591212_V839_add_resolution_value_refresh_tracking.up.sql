-- Track in-place value refreshes of an already-open rightsizing pull request.
--
-- When a recommendation moves materially while its pull request is still open,
-- the pull request is rewritten in place rather than left stale or duplicated
-- (#34959). Two guardrails need to persist across runs:
--
--   last_value_refresh_at  enforces a cooldown. The materiality threshold and the
--                          rule's buffer overlap, so a workload sitting near the
--                          line would otherwise rewrite its pull request on every
--                          hourly run.
--   value_refresh_count    caps how many times one pull request may be rewritten
--                          before we stop and leave it for a human, so a workload
--                          that never settles cannot churn a pull request forever.
--
-- Only recommendation_resolution needs these. event_resolution carries pull
-- requests too, but they come from incident remediation, which has no recurring
-- recommendation behind it to drift.

ALTER TABLE public.recommendation_resolution
    ADD COLUMN IF NOT EXISTS value_refresh_count integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_value_refresh_at timestamp;
