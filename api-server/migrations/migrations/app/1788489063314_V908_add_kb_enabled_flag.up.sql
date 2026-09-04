-- User-controlled on/off switch for a knowledge base.
--
-- Deliberately NOT another `status` value. `status` is the load lifecycle
-- (processing → active/error) and is owned by two automated writers: the
-- embedding pipeline, and the integration reconciler in
-- llm-server/tools/core/knowledgebase_sync.go, which flips integration KBs
-- between 'active' and 'archived' on every sync tick to track their
-- integration's eligibility. A user-set status would be reverted by the next
-- tick (default 30 min) and would also destroy the underlying load state.
-- `enabled` is orthogonal: a KB can be disabled while still indexing fine.
--
-- Lock window: ADD COLUMN ... DEFAULT is metadata-only on PG 11+ (no table
-- rewrite), so this takes a brief ACCESS EXCLUSIVE lock and returns. No
-- backfill needed — existing rows read the default and stay enabled.
ALTER TABLE llm_knowledgebases
ADD COLUMN IF NOT EXISTS enabled BOOLEAN NOT NULL DEFAULT TRUE;

COMMENT ON COLUMN llm_knowledgebases.enabled IS
'User switch. When false the KB is not searched, not listed as a loadable skill, and its content is never injected into an agent prompt. Independent of status, which tracks indexing.';
