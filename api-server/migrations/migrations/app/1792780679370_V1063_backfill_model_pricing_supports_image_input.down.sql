-- Intentionally a no-op. The up migration only fills NULLs, and nothing records which
-- rows were NULL before it ran, so clearing the flag here would also wipe values that
-- V830 or an admin had already set.
SELECT 1;
