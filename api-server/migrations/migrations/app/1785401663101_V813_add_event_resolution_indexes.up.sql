-- event_resolution has no indexes beyond the PK on id.
-- event_id is used in WHERE clauses and JOINs across event, pr_lifecycle,
-- and query services — every such query currently does a sequential scan.
CREATE INDEX IF NOT EXISTS idx_event_resolution_event_id
    ON event_resolution (event_id);

-- (status, resolver_type) is used by the periodic in-progress resolution
-- checker in event/service.go (WHERE status = $1 AND resolver_type = $2).
CREATE INDEX IF NOT EXISTS idx_event_resolution_status_resolver_type
    ON event_resolution (status, resolver_type);
