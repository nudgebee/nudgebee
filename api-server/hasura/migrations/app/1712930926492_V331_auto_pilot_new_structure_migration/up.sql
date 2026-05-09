
UPDATE
    auto_pilot
SET
    rule = jsonb_set(
        rule,
        '{resource_filter,id}',
        jsonb_build_object(
            'inclusions',
            jsonb_build_array(subquery.value),
            'exclusions',
            jsonb_build_array()
        ),
        true
    )
FROM
    (
        SELECT
            id,
            rule -> 'resource_filter' -> 'id' AS value
        FROM
            auto_pilot
        WHERE
            rule -> 'resource_filter' -> 'id' is not null
            and rule -> 'resource_filter' -> 'id' -> 'inclusions' is null
    ) AS subquery
WHERE
    auto_pilot.id = subquery.id;
