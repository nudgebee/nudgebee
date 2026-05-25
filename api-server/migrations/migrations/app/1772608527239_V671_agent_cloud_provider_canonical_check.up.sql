BEGIN;

ALTER TABLE agent
DROP CONSTRAINT IF EXISTS agent_cloud_provider_canonical_check;

ALTER TABLE agent
ADD CONSTRAINT agent_cloud_provider_canonical_check
CHECK (
  lower(type) NOT IN ('aws', 'azure', 'gcp')
  OR type IN ('AWS', 'Azure', 'GCP')
);

COMMIT;