UPDATE
    auto_playbook
SET
    tasks = (
        SELECT
            jsonb_agg(
                CASE
                    WHEN elem ->> 'type' = 'ticket_create'
                    and elem -> 'config' ->> 'description' is null THEN jsonb_set(
                        elem - 'ticket_body',
                        '{config,description}',
                        elem -> 'config' -> 'ticket_body'
                    )
                    ELSE elem
                END
            )
        FROM
            jsonb_array_elements(tasks) AS elem
    )
WHERE
    tasks @> '[{"type": "ticket_create"}]';

UPDATE
    auto_playbook
SET
    tasks = (
        SELECT
            jsonb_agg(
                CASE
                    WHEN elem ->> 'type' = 'ticket_create' THEN jsonb_set(
                        elem,
                        '{config}',
                        ((elem -> 'config') :: jsonb - 'ticket_body') :: jsonb
                    )
                    ELSE elem
                END
            )
        FROM
            jsonb_array_elements(tasks) AS elem
    )
WHERE
    tasks @> '[{"type": "ticket_create"}]';
