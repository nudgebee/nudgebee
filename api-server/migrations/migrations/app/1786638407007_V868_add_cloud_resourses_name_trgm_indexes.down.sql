-- pg_trgm is deliberately NOT dropped: other objects may depend on the
-- extension, and removing it to undo two indexes would be a far wider change
-- than this migration made.
--
-- Plain DROP (not CONCURRENTLY) for the same reason as the up migration — the
-- lint gate rejects CONCURRENTLY and migrations run in a transaction.

DROP INDEX IF EXISTS idx_cloud_resourses_resourseid_trgm;

DROP INDEX IF EXISTS idx_cloud_resourses_name_trgm;
