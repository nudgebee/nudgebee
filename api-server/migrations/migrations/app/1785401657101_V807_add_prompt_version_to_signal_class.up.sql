-- Add prompt_version to triage_signal_class so a prompt change can lazily re-mint stale verdicts.
-- A served verdict whose prompt_version < the current classificationPromptVersion is re-minted on
-- the next serve (feature-flagged). Pinned classes never reach the mint path (resolveHumanOverride
-- short-circuits before ComputeScoreLLM), so a human pin is never re-minted. Existing rows default
-- to 0 (older than any real prompt version), so the first flag-on serve re-mints them.
ALTER TABLE triage_signal_class ADD COLUMN IF NOT EXISTS prompt_version integer NOT NULL DEFAULT 0;
