ALTER TABLE event_duplicates ADD COLUMN absolute_first_seen_at timestamp;

-- Backfill from events table: compute absolute first seen per fingerprint
UPDATE event_duplicates ed
SET absolute_first_seen_at = sub.first_seen_at
FROM (
  SELECT cloud_account_id, tenant, fingerprint, MIN(created_at) as first_seen_at
  FROM events
  GROUP BY cloud_account_id, tenant, fingerprint
) sub
WHERE ed.cloud_account_id = sub.cloud_account_id
  AND ed.tenant_id = sub.tenant
  AND ed.fingerprint = sub.fingerprint;
