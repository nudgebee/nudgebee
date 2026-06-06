-- V757 rollback: drop draft_version_id. The strip falls back to a
-- definition-hash comparison against live (the pre-V747 behavior).
ALTER TABLE workflows
    DROP COLUMN IF EXISTS draft_version_id;
