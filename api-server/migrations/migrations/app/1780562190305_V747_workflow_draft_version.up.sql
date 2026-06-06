-- Migration: V747 — Track which version the current draft is based on
-- Description: Adds workflows.draft_version_id so the editor can show accurate
-- lineage ("Draft based on v2, Live is v3") after a user restores an older
-- version. Previously the strip computed "ahead of Live vX" by hashing
-- definitions, which lied when the draft branched off a non-live version.
--
-- Initial value for existing workflows: draft_version_id = live_version_id
-- (the draft is conceptually in sync with what's live until the user edits).
-- ON DELETE SET NULL is defensive — the version pruner protects both live and
-- draft pointers at the service layer.

ALTER TABLE workflows
    ADD COLUMN IF NOT EXISTS draft_version_id UUID REFERENCES workflow_versions(id) ON DELETE SET NULL;

UPDATE workflows
   SET draft_version_id = live_version_id
 WHERE draft_version_id IS NULL
   AND live_version_id IS NOT NULL;
