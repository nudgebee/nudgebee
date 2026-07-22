DROP INDEX IF EXISTS uq_workflow_templates_external_key;

ALTER TABLE workflow_templates
  DROP COLUMN IF EXISTS synced_at,
  DROP COLUMN IF EXISTS source_ref,
  DROP COLUMN IF EXISTS checksum,
  DROP COLUMN IF EXISTS external_key,
  DROP COLUMN IF EXISTS source;
