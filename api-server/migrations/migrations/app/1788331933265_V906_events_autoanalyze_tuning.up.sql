-- Keep the `events` planner statistics fresh so time-windowed dashboard queries
-- stop getting a rows=1 misestimate.
--
-- `events` is write-heavy and queue-shaped: ~2k inserts + ~24k updates + ~13k
-- deletes/day against a stable ~156k live rows (measured on dev over a 58-day
-- stats window). The global autoanalyze trigger is
--   50 + 0.10 * 156000 ~= 15,600 row changes
-- which at that churn fires only about once every 1-1.5 days (longer under
-- autovacuum-worker contention). So the created_at histogram's top bound trails
-- real time by more than a day, and any query filtering on the last 24h lands
-- ENTIRELY above the last histogram bucket. The planner then estimates 1 row
-- where there are thousands, picks nested-loop joins, and re-executes the
-- event_correlations incident-count aggregate once per matched event row.
--
-- Measured on dev, the event_groupings_v2 grouping query
-- (services/query/metadata.go) with a 24h window:
--   stale stats   EXPLAIN: Index Scan rows=1 (actual 2279) -> nested loops
--                 2,185,493 heap fetches, ~2,039,725 shared buffers, 2044 ms
--   after ANALYZE EXPLAIN: rows~=2653 (actual 2279) -> hash joins
--                 ~23,198 shared buffers, 150 ms   (~14x faster)
--
-- Fix: lower ONLY the autoanalyze trigger for this one table (per-table
-- reloptions; every other table keeps the global defaults). The new trigger is
--   500 + 0.02 * 156000 ~= 3,600 row changes
-- i.e. autoanalyze roughly every 2-3h, keeping the histogram within a few hours
-- of real time. This is proportional (scale_factor, not a flat threshold) so it
-- travels to larger prod tables and to small on-prem installs alike; the 500
-- floor keeps tiny installs from never triggering.
--
-- Cost of one autoanalyze here: a ~30k-row sample (statistics_target is left at
-- the default 100), a few seconds of throttled background I/O, SHARE UPDATE
-- EXCLUSIVE only -- it does NOT block the application's SELECT/INSERT/UPDATE.
-- VACUUM settings are deliberately NOT touched: dead tuples sit near 7% (well
-- under the 20% trigger) and vacuuming `events` also walks its ~94 GB TOAST
-- table, so making it hair-trigger would starve the 3 shared autovacuum workers.
--
-- All three statements below are transaction-safe (verified on PG 17): plain
-- ANALYZE -- unlike VACUUM -- runs inside a transaction block, which golang-
-- migrate/atlas requires since it wraps every migration file in one.

SET LOCAL lock_timeout = '5s';

ALTER TABLE events SET (
  autovacuum_analyze_scale_factor = 0.02,
  autovacuum_analyze_threshold    = 500
);

-- Apply the fix now instead of waiting for the first autoanalyze (~2-3h).
ANALYZE events;
