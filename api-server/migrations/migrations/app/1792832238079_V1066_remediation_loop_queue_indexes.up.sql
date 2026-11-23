-- Remediation loop (#40734): the leader's follow-up and planning queues filter on JSON state of event_resolution
-- and event_log_analysis rows, and triage's duplicate check reads an alert's resolves for the loop. These partial
-- indexes keep each read to the rows it can act on, however large the tables grow.
--
-- Plain CREATE INDEX: CONCURRENTLY cannot run in a migration (see CLAUDE.md). Build them out of band with
-- CREATE INDEX CONCURRENTLY IF NOT EXISTS before this deploys; then this file is a no-op. Where it is not
-- pre-built (on-prem), each statement holds a SHARE lock on its table (writes wait, reads go on) for one scan of
-- it: seconds on event_resolution and event_log_analysis, about as long as one sequential scan of events for the
-- last one.

-- ClaimDueFollowups: settled runs created in the last hour with no follow-up yet.
CREATE INDEX IF NOT EXISTS idx_event_resolution_followup_unclaimed
    ON event_resolution (created_at)
    WHERE type = 'CommandExecution' AND (data->'followup') IS NULL;

-- OpenFollowups and ClaimDueRechecks: follow-ups that have not ended.
CREATE INDEX IF NOT EXISTS idx_event_resolution_followup_open
    ON event_resolution ((data->'followup'->>'recheck_at'))
    WHERE type = 'CommandExecution' AND (data->'followup') IS NOT NULL AND (data->'followup'->>'done_at') IS NULL;

-- The follow-up of a conversation turn (by its message id).
CREATE INDEX IF NOT EXISTS idx_event_resolution_followup_message
    ON event_resolution ((data->'followup'->>'message_id'))
    WHERE (data->'followup'->>'message_id') IS NOT NULL;

-- OpenPlannings and PlanningOfTurn: planning requests only, so the text-to-jsonb read of their state runs on those
-- rows alone. No JSON in the index itself: a row that is not JSON cannot fail the build.
CREATE INDEX IF NOT EXISTS idx_event_log_analysis_planning
    ON event_log_analysis (cloud_account_id, recorded_at)
    WHERE analysis_type = 'remediation_planning';

-- detectAndRecordDuplicate (flag on): an alert's events resolved for the loop.
CREATE INDEX IF NOT EXISTS idx_events_loop_resolved
    ON events (fingerprint, cloud_account_id, nb_status_changed_at DESC)
    WHERE nb_status = 'RESOLVED' AND labels ? 'nb_resolved_by';
