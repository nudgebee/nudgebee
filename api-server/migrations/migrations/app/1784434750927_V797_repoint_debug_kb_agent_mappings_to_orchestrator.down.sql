-- Irreversible data consolidation: rows originally keyed to a *_debug name and rows
-- keyed to the canonical *_orchestrator name are merged onto the canonical key, and
-- redundant *_debug rows are deleted. The original split cannot be reconstructed
-- (we can't know which canonical rows were pre-existing vs migrated), so there is no
-- safe automatic down. The code fallback reads both names, so a rollback of the
-- application does not require reverting this data. No-op.
SELECT 1;
