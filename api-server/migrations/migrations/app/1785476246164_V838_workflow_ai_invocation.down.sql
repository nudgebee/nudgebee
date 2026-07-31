-- Remove per-tenant/account enrolments first (feature_flag.feature_id FKs the
-- catalog row), then the catalog entry, then the columns.

DELETE FROM public.feature_flag WHERE feature_id = 'AI_WORKFLOW_TOOLS';
DELETE FROM public.feature WHERE value = 'AI_WORKFLOW_TOOLS';

DROP INDEX IF EXISTS idx_workflows_ai_invocable;

ALTER TABLE workflows
    DROP COLUMN IF EXISTS ai_invocable,
    DROP COLUMN IF EXISTS description;
