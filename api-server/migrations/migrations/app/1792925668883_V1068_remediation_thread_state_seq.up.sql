-- Remediation loop in the alert's Slack thread (#40734): ordering of the thread's edited messages.
--
-- remediation_thread_state_seq numbers every sync of an alert's thread (llm-server takes the next value before it
-- reads the alert's state), so notifications-server can refuse a sync older than the one a message already shows.
--
-- The unique index makes one marker row per message key: a marker is written before its message is posted, and a
-- second writer for the same key now fails on the insert instead of posting the message twice.
--
-- Lock window: CREATE UNIQUE INDEX (not CONCURRENTLY, which cannot run in a migration's transaction) blocks writes to
-- sent_notifications while it scans the table. On a large table, build it out-of-band first with
--   CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_sent_notifications_remediation_marker
--     ON sent_notifications (tenant_id, fingerprint) WHERE kind = 'remediation_marker';
-- after the same DELETE below, and this file is then a no-op for it.
CREATE SEQUENCE IF NOT EXISTS remediation_thread_state_seq;

-- Duplicate markers (possible before this index: the claim that kept them apart fails open) are only markers; the
-- newest of each key is kept.
DELETE FROM sent_notifications a
 USING sent_notifications b
 WHERE a.kind = 'remediation_marker' AND b.kind = 'remediation_marker'
   AND a.tenant_id = b.tenant_id AND a.fingerprint = b.fingerprint
   AND (a.created_at, a.id) < (b.created_at, b.id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_sent_notifications_remediation_marker
    ON sent_notifications (tenant_id, fingerprint) WHERE kind = 'remediation_marker';
