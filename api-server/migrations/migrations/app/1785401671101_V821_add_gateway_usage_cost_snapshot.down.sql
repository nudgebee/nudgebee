-- No-op. The up uses ADD COLUMN IF NOT EXISTS, so a rollback must not drop the
-- column (it may be present on a healthy/drifted environment). Leaving cost_usd in
-- place is harmless — it defaults to 0 and pre-snapshot code ignores it. Matches the
-- repo convention for additive ADD COLUMN migrations (e.g. V214).
SELECT 1;
