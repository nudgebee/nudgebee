-- event_groupings_v2 (the K8s / Events grouping listing) intermittently takes
-- 5-6s on a cold load, ~640ms warm. Root cause, measured on dev (tenant
-- 890cad87..., ~51k events in a 30-day tenant-wide grouping): the
--   LEFT JOIN event_duplicates ed
--     ON ed.event_id = events.id AND ed.cloud_account_id = events.cloud_account_id
-- reads ed.absolute_first_seen_at and ed.occurrence_number, neither of which is
-- in the only usable index (event_duplicates_event_id_cloud_account_id_key,
-- keyed on (event_id, cloud_account_id) alone). So every matched event does an
-- index lookup + a random heap fetch; a broad grouping matches ~51k events, so
-- ~51k random reads. Cold that is the 5-6s; warm the heap pages are cached so
-- it is ~640ms -- hence the intermittency. At random_page_cost = 1.5 the
-- planner takes that per-row nested loop (5.8s); at 4.0 it full-scans
-- event_duplicates instead (2.5s) -- both slow, 1.5 the worse of the two.
--
-- Fix: an INCLUDE covering index so the lookup is index-only (zero heap
-- fetches). Measured on dev: 5,798ms -> 687ms at random_page_cost = 1.5, and
-- the query stops being random_page_cost-sensitive (687 vs 594 at 4.0). Other
-- event surfaces that join event_duplicates on the same keys benefit too.
-- occurrence_number is bumped when a duplicate recurs, so covering it makes
-- those UPDATEs non-HOT -- negligible here (event_duplicates is 318MB / ~104k
-- rows, low update rate).
--
-- OPERATOR NOTE -- run once per environment, out of band, around merge
-- (migrations-lint.yaml rejects CONCURRENTLY in migration SQL):
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_event_dup_evt_acct_cover
--     ON event_duplicates (event_id, cloud_account_id)
--     INCLUDE (absolute_first_seen_at, occurrence_number);
-- dev already has it applied. Where it was pre-applied the statement below is a
-- no-op; where it was not (on-prem self-hosted), the plain CREATE INDEX holds a
-- SHARE lock on event_duplicates for the build (~seconds at 318MB), blocking
-- writes but not reads.

CREATE INDEX IF NOT EXISTS idx_event_dup_evt_acct_cover
  ON event_duplicates (event_id, cloud_account_id)
  INCLUDE (absolute_first_seen_at, occurrence_number);

ANALYZE event_duplicates;
