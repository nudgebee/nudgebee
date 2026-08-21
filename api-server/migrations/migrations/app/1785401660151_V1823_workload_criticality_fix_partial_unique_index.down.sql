-- Intentionally a no-op.
-- OSS ordering note: paired with the post-schema repair at version 1785401660151.
--
-- The up migration converges a drifted index onto the shape V776 committed. Restoring the partial
-- predicate would re-break every upsert against this table, so there is nothing worth rolling back
-- to. Dropping the index outright is also wrong — the committed schema requires it.
SELECT 1;
