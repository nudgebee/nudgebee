UPDATE
    auto_pilot
SET
    rule = jsonb_set(rule, '{analysis_duration_hour}', '168' :: jsonb)
WHERE
    category = 'continuous_rightsize';
