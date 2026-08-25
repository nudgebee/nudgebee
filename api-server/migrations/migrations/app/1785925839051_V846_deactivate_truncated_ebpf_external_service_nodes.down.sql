-- No-op. The up migration soft-deletes (is_active=false) stale, collapsed
-- ExternalService rows; it does not record which rows it touched, and other
-- rows may legitimately share the same name pattern while inactive for
-- unrelated reasons. Blindly re-activating by pattern could resurrect rows
-- that should stay inactive. Recovery is the authoritative per-tenant graph
-- rebuild, which re-creates every legitimately-observed external node.
SELECT 1;
