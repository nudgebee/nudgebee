-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- ALTER TABLE agent
-- ADD CONSTRAINT agent_cloud_provider_canonical_check
-- CHECK (
--   lower(type) NOT IN ('aws', 'azure', 'gcp')
--   OR type IN ('AWS', 'Azure', 'GCP')
-- );

ALTER TABLE agent
DROP CONSTRAINT IF EXISTS agent_cloud_provider_canonical_check;
