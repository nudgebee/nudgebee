-- Intentionally does not delete integrations rows: if Discord installs exist,
-- the FK blocks this rollback loudly rather than silently dropping them.
DELETE FROM integration_types WHERE name = 'discord';
