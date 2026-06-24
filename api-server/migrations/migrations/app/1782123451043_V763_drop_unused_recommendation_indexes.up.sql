-- Drop three unused indexes on the `recommendation` table to cut per-cycle
-- autovacuum index-scan work on this 60 GB table. All three confirmed unused on
-- prod (idx_scan = 0 over the full stats window):
--   * idx_recommendation_security_status_weight  (654 MB, V738) — partial image_scan
--     severity-weight ordering index; planner prefers idx_recommendation_security_account_image_name.
--   * recommendation_dedupe_group_idx             (40 kB,  V714) — dedupe_group is null
--     for ~all rows; the dedupe_group column stays, only the index is dropped.
--   * idx_recommendation_security_acct_image_full (0 bytes)       — INVALID/not-ready stub
--     left by a failed CREATE INDEX CONCURRENTLY; never created by a migration.
--
-- Plain DROP INDEX (NOT concurrent): golang-migrate runs each migration inside a
-- transaction, and DROP INDEX CONCURRENTLY cannot run in one (it ignores the
-- dbmate-style "migrate:no-transaction" hint). DROP INDEX takes a brief
-- ACCESS EXCLUSIVE lock but only unlinks catalog entries (no scan), so it is
-- fast even on a large table. On prod these were already dropped out-of-band
-- with DROP INDEX CONCURRENTLY, so here they are no-ops (IF EXISTS).

DROP INDEX IF EXISTS idx_recommendation_security_status_weight;
DROP INDEX IF EXISTS recommendation_dedupe_group_idx;
DROP INDEX IF EXISTS idx_recommendation_security_acct_image_full;
