
UPDATE
    auto_playbook_task
SET
    attributes = jsonb_build_object(
        'trigger_context',
        jsonb_build_object('trigger_type', '')
    );

update
    auto_playbook_task
set
    attributes = jsonb_set(
        CASE
            WHEN apt.attributes ? 'trigger_context' THEN apt.attributes
            ELSE jsonb_build_object('trigger_context', '{}')
        END,
        '{trigger_context,trigger_type}',
        to_jsonb(
            (
                SELECT
                    jsonb_object_keys(trigger)
                FROM
                    auto_playbook_task
                WHERE
                    id = apt.id
                LIMIT
                    1
            )
        ), true
    )
FROM
    auto_playbook_task apt
    JOIN auto_playbook ap ON apt.auto_playbook_id = ap.id
WHERE
    apt.attributes -> 'trigger_type' IS NULL;
