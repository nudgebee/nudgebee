
INSERT INTO
    autopilot_attributes (key, value)
VALUES
    (
        'last_event_scan_time',
        CURRENT_TIMESTAMP AT TIME ZONE 'UTC'
    );
