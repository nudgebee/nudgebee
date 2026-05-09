
UPDATE
    auto_pilot
SET
    rule = jsonb_set(
        rule,
        '{rules,memory,trigger,max_change_pct}',
        'null' :: jsonb,
        true
    )
WHERE
    category = 'vertical_rightsize'
    and rule -> 'rules' -> 'memory' -> 'trigger' -> 'max_change_pct' is null;

UPDATE
    auto_pilot
SET
    rule = jsonb_set(
        rule,
        '{rules,cpu,trigger,max_change_pct}',
        'null' :: jsonb,
        true
    )
WHERE
    category = 'vertical_rightsize'
    and rule -> 'rules' -> 'cpu' -> 'trigger' -> 'max_change_pct' is null;

UPDATE
    auto_pilot
SET
    rule = jsonb_set(
        rule,
        '{rules,cpu,min_cpu}',
        'null' :: jsonb,
        true
    )
WHERE
    category = 'vertical_rightsize'
    and rule -> 'rules' -> 'cpu' -> 'min_cpu' is null;

UPDATE
    auto_pilot
SET
    rule = jsonb_set(
        rule,
        '{rules,cpu,max_cpu}',
        'null' :: jsonb,
        true
    )
WHERE
    category = 'vertical_rightsize'
    and rule -> 'rules' -> 'cpu' -> 'max_cpu' is null;

UPDATE
    auto_pilot
SET
    rule = jsonb_set(
        rule,
        '{rules,memory,min_memory}',
        'null' :: jsonb,
        true
    )
WHERE
    category = 'vertical_rightsize'
    and rule -> 'rules' -> 'memory' -> 'min_memory' is null;

UPDATE
    auto_pilot
SET
    rule = jsonb_set(
        rule,
        '{rules,memory,max_memory}',
        'null' :: jsonb,
        true
    )
WHERE
    category = 'vertical_rightsize'
    and rule -> 'rules' -> 'memory' -> 'max_memory' is null;
