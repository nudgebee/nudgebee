-- Intentionally a no-op.
--
-- The up migration converges a drifted index onto the shape V776 committed. Restoring the partial
-- predicate would re-break every upsert against this table, so there is nothing worth rolling back
-- to. Dropping the index outright is also wrong — the committed schema requires it.
SELECT 1;
