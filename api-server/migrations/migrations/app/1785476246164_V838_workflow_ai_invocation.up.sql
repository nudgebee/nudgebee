-- Workflow metadata for AI (Nubi) invocation.
--
-- Two workflow-level columns plus the feature-catalog entry that gates the
-- whole capability.
--
-- ai_invocable is deliberately a COLUMN and not a field inside the versioned
-- `definition` JSONB. Definition fields ride the draft -> publish -> make-live
-- cycle, so revoking AI access by editing the definition would not take effect
-- until the user republished — and the listing UI (which reads the draft
-- definition) would show the toggle OFF while the live version was still
-- invocable. A column flips instantly, survives a version rollback, and mirrors
-- how `status` gates execution today.
--
-- description is the human-facing workflow description. The workflow YAML has
-- accepted a top-level `description:` key all along (see the integration test
-- fixtures) but it was silently dropped on the floor because model.Workflow had
-- no field for it. This column gives it a home.
--
-- The AI-facing description (llm_description) and keywords (llm_keywords) live
-- INSIDE the definition JSONB on purpose: they are content that should snapshot
-- with a published version, so the AI sees what is live rather than an
-- in-progress draft. No DDL is needed for them.

ALTER TABLE workflows
    ADD COLUMN IF NOT EXISTS ai_invocable boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS description text;

COMMENT ON COLUMN workflows.ai_invocable IS
    'Opt-in: may the AI assistant invoke this workflow. Enforced server-side on every AI-originated trigger (X-Trigger-Source: ai). Workflow-level, not versioned, so revocation is immediate.';
COMMENT ON COLUMN workflows.description IS
    'Human-facing workflow description (top-level `description:` in workflow YAML).';

-- Partial index for the AI-tool discovery listing, which only ever selects
-- opted-in workflows. Partial (WHERE ai_invocable) keeps it tiny: the expected
-- shape is a handful of opted-in workflows per account out of many.
CREATE INDEX IF NOT EXISTS idx_workflows_ai_invocable
    ON workflows (account_id)
    WHERE ai_invocable;

-- Register the feature in the existing catalog. Registering does NOT enable it:
-- with no public.feature_flag row the gate resolves to false, the discovery
-- listing returns empty, and no workflow tools are synthesized — the feature is
-- inert by default and opt-in per tenant or per account.
--
-- To enable for a tenant:
--   INSERT INTO public.feature_flag (feature_id, tenant_id, status)
--   VALUES ('AI_WORKFLOW_TOOLS', '<tenant-uuid>', 'enabled');
-- To enable for a single account (pilot):
--   INSERT INTO public.feature_flag (feature_id, tenant_id, account_id, status)
--   VALUES ('AI_WORKFLOW_TOOLS', '<tenant-uuid>', '<account-uuid>', 'enabled');
-- To turn it back off (per-workflow opt-ins stay, they just stop applying):
--   DELETE FROM public.feature_flag WHERE feature_id = 'AI_WORKFLOW_TOOLS' AND ...;

INSERT INTO public.feature (value, description)
VALUES (
    'AI_WORKFLOW_TOOLS',
    'Lets the AI assistant discover and invoke automations that have been explicitly opted in (workflows.ai_invocable), and run curated built-in automation actions. Every AI-initiated run still requires in-chat user confirmation and the user''s own permissions.'
)
ON CONFLICT (value) DO NOTHING;
