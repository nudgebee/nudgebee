-- Intentionally does not delete messaging_platforms rows: if Discord installs
-- exist, the FK blocks this rollback loudly rather than silently dropping them.
DELETE FROM "public"."messaging_platforms_type" WHERE "value" = 'discord';
