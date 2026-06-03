
UPDATE
    auto_playbook_task
SET
    resource = jsonb_build_array(resource)
WHERE
    jsonb_typeof(resource) = 'object';
