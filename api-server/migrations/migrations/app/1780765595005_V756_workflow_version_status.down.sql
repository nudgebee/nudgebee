-- V756 rollback: drop the per-version status column. workflows.status (kept
-- in lockstep with the live version's status) is the authoritative gate again.
ALTER TABLE workflow_versions
    DROP COLUMN IF EXISTS status;
