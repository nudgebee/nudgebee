-- Retire recommendations stuck in InProgress with no resolution behind them.
--
-- Every writer that claims a recommendation inserts its recommendation_resolution row
-- before flipping status, so InProgress with no resolution means nobody is working it.
-- Those rows were unreachable: the settle query joins recommendation_resolution, and
-- every archive path skips InProgress, so they stayed visible in Optimise indefinitely.
--
-- Dead resource -> Archive. Live (or resource-less) -> Open, handing the row back to its
-- producer's normal archive cycle. The api-server cron applies the same rules from now on;
-- this does it once at deploy so the backlog does not wait on two cron hops.
--
-- Also converts the replica_right_sizing working set, which used InProgress as its
-- "enrolled for refresh" marker. The ml-k8s-server refresh now selects Open, so the live
-- rows must move with it or the 12h refresh finds an empty working set.

UPDATE recommendation r
SET status = 'Archive', updated_at = NOW()
FROM cloud_resourses cr
WHERE cr.id = r.resource_id
  AND cr.is_active = false
  AND r.status = 'InProgress'
  AND NOT EXISTS (
    SELECT 1 FROM recommendation_resolution rr WHERE rr.recommendation_id = r.id
  );

UPDATE recommendation r
SET status = 'Open', updated_at = NOW()
WHERE r.status = 'InProgress'
  AND NOT EXISTS (
    SELECT 1 FROM recommendation_resolution rr WHERE rr.recommendation_id = r.id
  );
