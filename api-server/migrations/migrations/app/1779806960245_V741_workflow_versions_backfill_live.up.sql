-- V741: Backfill live workflow_versions for legacy workflows so existing
-- automations can be triggered (manual, scheduled, event) without a manual
-- Publish click. Only touches rows that have NO existing version row AND
-- live_version_id IS NULL — safe to re-run, idempotent.
--
-- Also rewrites chk_workflow_version_source to drop 'save', which was declared
-- in V740 but never written by any production code path. Existing 'save' rows
-- (defensive — there shouldn't be any) are migrated to 'publish' so the new
-- constraint accepts them. Canvas "Run current" executes the draft directly and
-- snapshots no version row, so no 'draft_run' source is needed.

UPDATE workflow_versions SET source = 'publish' WHERE source = 'save';

ALTER TABLE workflow_versions DROP CONSTRAINT IF EXISTS chk_workflow_version_source;
ALTER TABLE workflow_versions
    ADD CONSTRAINT chk_workflow_version_source
    CHECK (source IN ('create', 'publish', 'restore'));

WITH targets AS (
    SELECT w.id, w.definition, w.created_by, w.created_at
    FROM workflows w
    LEFT JOIN workflow_versions v ON v.workflow_id = w.id
    WHERE w.live_version_id IS NULL
    GROUP BY w.id
    HAVING COUNT(v.id) = 0
), inserted AS (
    INSERT INTO workflow_versions
        (workflow_id, version_number, definition, source, created_by, created_at)
    SELECT id, 1, definition, 'create', created_by, COALESCE(created_at, now())
    FROM targets
    RETURNING id, workflow_id
)
UPDATE workflows w
SET live_version_id = i.id
FROM inserted i
WHERE w.id = i.workflow_id;
