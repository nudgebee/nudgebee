ALTER TABLE events ADD COLUMN IF NOT EXISTS nb_status TEXT DEFAULT 'OPEN'
    CHECK (nb_status IN ('OPEN', 'ACKNOWLEDGED', 'INVESTIGATING', 'SNOOZED', 'SUPPRESSED', 'DROPPED', 'RESOLVED'));

ALTER TABLE events ADD COLUMN IF NOT EXISTS nb_status_changed_at TIMESTAMP;
ALTER TABLE events ADD COLUMN IF NOT EXISTS nb_status_changed_by UUID;
ALTER TABLE events ADD COLUMN IF NOT EXISTS snoozed_until TIMESTAMP;

-- Index for filtering by nb_status
CREATE INDEX IF NOT EXISTS idx_events_nb_status ON events(nb_status, cloud_account_id);
CREATE INDEX IF NOT EXISTS idx_events_snoozed_until ON events(snoozed_until) WHERE snoozed_until IS NOT NULL;

-- Backfill existing events
UPDATE events SET nb_status = 'RESOLVED' WHERE status = 'CLOSED' AND nb_status IS NULL;
UPDATE events SET nb_status = 'OPEN' WHERE nb_status IS NULL;
