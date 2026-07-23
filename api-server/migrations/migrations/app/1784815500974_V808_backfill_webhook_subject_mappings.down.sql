-- Irreversible: merged services in webhook_subject_mappings can't be
-- cleanly attributed back to "came from this backfill" vs. pre-existing /
-- live-learned entries. No-op down. tenant_attrs (the source data) was
-- never modified by the up migration, so it remains available as the
-- source of truth if a manual cleanup is ever needed.
SELECT 1;
