-- Reverse the slug backfill. (The source column itself is dropped by V766 down.)
-- Not filtered by source: a sync may have flipped source to bundled/github, but the
-- external_key still originated from this backfill, so clear it on all system rows.
UPDATE workflow_templates
SET external_key = NULL,
    synced_at = NULL
WHERE is_system = true AND tenant_id IS NULL AND account_id IS NULL;
