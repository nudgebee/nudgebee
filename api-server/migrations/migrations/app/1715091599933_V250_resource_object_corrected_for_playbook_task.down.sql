
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- UPDATE
--     auto_playbook_task
-- SET
--     resource = jsonb_build_array(resource)
-- WHERE
--     jsonb_typeof(resource) = 'object';
