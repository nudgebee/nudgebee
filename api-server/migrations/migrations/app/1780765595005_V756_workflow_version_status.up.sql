-- Migration: V756 — Per-version status (Active / Paused / Inactive)
-- Description: Move the runtime gate from workflows.status onto each
-- workflow_versions row so a user can publish a new version directly into a
-- Paused state (the previous model auto-activated). workflows.status is kept
-- as a derived mirror of the live version's status; service code re-syncs it
-- on every SetLiveVersion and UpdateVersionStatus on the live version. Default
-- is PAUSED so brand-new workflows (and their v1) require an explicit activate.

ALTER TABLE workflow_versions
    ADD COLUMN IF NOT EXISTS status VARCHAR(50) NOT NULL DEFAULT 'PAUSED';

-- Backfill existing rows from the parent workflow.status. ACTIVE workflows
-- keep firing (no surprise pauses for existing customers); PAUSED / INACTIVE
-- propagate accordingly. Only rows still at the column default are rewritten,
-- so the migration is safe to re-run.
UPDATE workflow_versions wv
   SET status = COALESCE(w.status, 'PAUSED')
  FROM workflows w
 WHERE wv.workflow_id = w.id
   AND wv.status = 'PAUSED';
