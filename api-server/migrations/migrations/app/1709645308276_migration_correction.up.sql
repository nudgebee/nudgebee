delete from
    auto_pilot_execution_status
where
    value = 'Disabled';

INSERT INTO
    "public"."auto_playbook_status"("value", "description")
VALUES
    ('Disabled', 'the auto playbook is in disabled')
ON CONFLICT (value) DO NOTHING;