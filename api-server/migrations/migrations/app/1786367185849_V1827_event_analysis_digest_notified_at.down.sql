-- Only safe while no rule references it; the FK blocks the delete otherwise,
-- which is the correct outcome rather than orphaning a tenant's rule.
DELETE FROM notification_source_type WHERE value = 'weekly_digest';

DROP INDEX IF EXISTS idx_event_analysis_digest_pending_notify;

ALTER TABLE event_analysis_digest
    DROP COLUMN IF EXISTS notified_at;
