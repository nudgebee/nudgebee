-- Reverse V740
-- Drop the live pointer first; its FK references workflow_versions, so the
-- table cannot be dropped while the column exists.
ALTER TABLE workflows DROP COLUMN IF EXISTS live_version_id;

DROP TABLE IF EXISTS workflow_versions;
