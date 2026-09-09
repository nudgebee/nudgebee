-- Close the backlog of Anomaly/SLO events that predate the fix giving these
-- producers a stable finding_id and a close-on-recovery path. Before that fix,
-- every detection cycle wrote a brand-new event row (finding_id was a fresh
-- UUID / slo_report id each cycle), so these rows could never transition out
-- of FIRING no matter how long ago the condition actually cleared.
--
-- category = 'Anomaly' scopes the Anomaly branch to K8s metric anomalies only.
-- Spend anomalies also use finding_type = 'Anomaly' but category = 'CostAnomaly'
-- and already have a working OPEN/RESOLVED lifecycle (spend_anomaly.go) — this
-- must not touch those, or it would force-close live, still-open spend
-- anomalies that just haven't recovered yet.
--
-- Using CLOSED (not RESOLVED), matching V884's reasoning: these rows' true
-- recovery state is unknown, so "closed" (administratively ended) is the
-- honest label, not "resolved" (confirmed recovered).
--
-- One statement, one transaction, no index build. Bounded by status='FIRING'
-- plus narrow finding_type/category predicates. Expected lock window:
-- sub-second to low-second depending on backlog size. No CONCURRENTLY, so
-- nothing to pre-apply out-of-band.
--
-- updated_at is `timestamp` (no timezone, see V175). now() is timestamptz;
-- assigning it directly casts through the session's timezone setting, so a
-- non-UTC session would write a non-UTC wall-clock value into a column every
-- other write in this codebase treats as UTC. AT TIME ZONE 'UTC' pins it.
UPDATE events
   SET status     = 'CLOSED',
       ends_at    = COALESCE(ends_at, starts_at),
       nb_status  = CASE
                      WHEN nb_status IN ('OPEN', 'ACKNOWLEDGED', 'INVESTIGATING', 'ACTION_REQUIRED')
                      THEN 'RESOLVED' ELSE nb_status
                    END,
       updated_at = (now() AT TIME ZONE 'UTC')
 WHERE status = 'FIRING'
   AND (
         finding_type = 'SLO'
         OR (finding_type = 'Anomaly' AND category = 'Anomaly')
       );
