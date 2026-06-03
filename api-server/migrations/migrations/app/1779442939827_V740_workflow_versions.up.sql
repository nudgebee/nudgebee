-- Migration: Workflow versioning (publish / live-version model)
-- Version: V740
-- Description: Create workflow_versions (immutable definition snapshots) and add
-- workflows.live_version_id, the pointer to the version every execution runs.
-- The table is created with its final shape (name/description metadata, source
-- enum including 'publish'); existing workflows get live_version_id = NULL until
-- their first publish.

CREATE TABLE IF NOT EXISTS workflow_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id UUID NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    version_number INT NOT NULL,
    definition JSONB NOT NULL,
    source VARCHAR(20) NOT NULL,
    restored_from_version INT,
    name VARCHAR(255),
    description TEXT,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uniq_workflow_version UNIQUE (workflow_id, version_number),
    CONSTRAINT chk_workflow_version_source CHECK (source IN ('create', 'save', 'publish', 'restore'))
);

CREATE INDEX IF NOT EXISTS idx_workflow_versions_wf_desc
    ON workflow_versions (workflow_id, version_number DESC);

-- Live version pointer on workflows (independent of workflows.status).
-- ON DELETE SET NULL is defensive — service-layer code refuses to delete a
-- version that is currently live.
ALTER TABLE workflows
    ADD COLUMN IF NOT EXISTS live_version_id UUID REFERENCES workflow_versions(id) ON DELETE SET NULL;
