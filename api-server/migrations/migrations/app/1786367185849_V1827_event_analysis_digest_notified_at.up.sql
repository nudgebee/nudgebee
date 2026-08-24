-- When this tenant-week was delivered to notification channels.
--
-- Delivery is tracked separately from generation because the digest job is
-- convergent: FindPendingDigestPeriods re-queues any row whose metrics, briefing
-- or finding shape key is superseded, so a prompt change deliberately
-- regenerates the whole lookback window. Keying delivery off status='generated'
-- would re-send every one of those weeks to every channel. UpsertDigest does not
-- touch this column, so a regenerated week keeps its delivery mark.
ALTER TABLE event_analysis_digest
    ADD COLUMN IF NOT EXISTS notified_at timestamptz;

-- Rows that already exist were generated before delivery was possible. Stamping
-- them keeps the first deploy from firing the entire lookback window (3 weeks x
-- every tenant) into Slack at once; delivery begins with the next week.
UPDATE event_analysis_digest SET notified_at = now() WHERE notified_at IS NULL;

-- Serves the delivery sweep, which asks for undelivered scheduled rows only.
-- Partial so it holds just the handful of rows actually pending at any moment
-- rather than the whole table, which is otherwise 100% delivered.
CREATE INDEX IF NOT EXISTS idx_event_analysis_digest_pending_notify
    ON event_analysis_digest (period_start DESC)
    WHERE notified_at IS NULL;

-- notification_rules.source is a foreign key into this lookup table, so the new
-- source has to exist here before anyone can save a rule for it. Without this
-- row the Weekly Digest tab renders, accepts input, and then fails the
-- insert with a foreign-key violation.
INSERT INTO notification_source_type (value)
VALUES ('weekly_digest')
ON CONFLICT DO NOTHING;
