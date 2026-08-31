-- Widen the image_scan lookup index so the scan_orchestrator archive UPDATE can
-- actually use it.
--
-- V733 created idx_recommendation_security_account_image_name with the predicate
--   WHERE category='Security' AND rule_name='image_scan' AND account_object_id IS NOT NULL
-- for the query-engine paths in services/query/metadata.go, which all filter on
-- account_object_id.
--
-- The per-image archive UPDATE in services/scan_orchestrator/persist.go does NOT
-- filter on account_object_id, so Postgres cannot prove the index predicate holds
-- and refuses the index -- despite a code comment there claiming it "rides" it.
-- It falls back to idx_recommendation_tenant_account_status and evaluates
-- recommendation->>'image_name' as a post-scan Filter, detoasting the JSONB for
-- every image_scan row in the account.
--
-- Measured on dev, one account/image (EXPLAIN ANALYZE on the equivalent SELECT):
--   as written                            cost 51772, 4081 ms, 18042 rows scanned, 8137 discarded
--   + AND account_object_id IS NOT NULL   cost   400, index cond on image_name
-- a 129x planner-cost reduction from that predicate alone.
--
-- Fixing it in the query instead would mean adding account_object_id IS NOT NULL
-- to the UPDATE, which changes archive semantics -- rows with a NULL
-- account_object_id would silently never archive. Widening the index keeps
-- semantics identical.
--
-- Dropping the predicate is safe for the existing consumers: a partial index with
-- a WEAKER predicate still serves queries whose own predicate implies it, so the
-- metadata.go paths keep using it (their account_object_id test becomes a cheap
-- recheck). On dev only 1 of 512612 image_scan rows has a NULL account_object_id,
-- so the index grows by a single entry.
--
-- ---------------------------------------------------------------------------
-- OPERATOR NOTE -- pre-apply concurrently on every environment BEFORE merging.
--
-- `recommendation` is ~2.8 GB on dev and ~60 GB on prod. The statements below are
-- NOT concurrent (migrations-lint.yaml rejects CONCURRENTLY in executable SQL),
-- so run these by hand first, per that workflow's own instructions; the migration
-- then finds the work already done and is a cheap no-op:
--
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_recommendation_image_scan_account_image_name
--   ON recommendation (cloud_account_id, tenant_id, (recommendation->>'image_name'))
--   WHERE category = 'Security' AND rule_name = 'image_scan';
--
--   DROP INDEX CONCURRENTLY IF EXISTS idx_recommendation_security_account_image_name;
--
-- Build BEFORE dropping so the lookup path (2.7M scans per stats window on dev) is
-- never left without an index. If a concurrent build fails it leaves an INVALID
-- index behind -- drop it and retry (see V752, which cleaned up exactly that).
-- ---------------------------------------------------------------------------

CREATE INDEX IF NOT EXISTS idx_recommendation_image_scan_account_image_name
ON recommendation (cloud_account_id, tenant_id, (recommendation->>'image_name'))
WHERE category = 'Security'
  AND rule_name = 'image_scan';

DROP INDEX IF EXISTS idx_recommendation_security_account_image_name;
