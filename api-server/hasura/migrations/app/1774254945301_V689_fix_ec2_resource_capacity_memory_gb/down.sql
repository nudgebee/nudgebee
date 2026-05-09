-- Down migration: no rollback — converting numeric back to string would require knowing
-- original unit, which is not preserved. These rows should remain as numeric values.
SELECT 1;
