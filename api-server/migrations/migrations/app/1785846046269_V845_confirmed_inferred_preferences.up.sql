-- Records that an explicit preference got there by the user confirming an
-- inference ("Keep" in b-Cortex), rather than by typing a value into
-- Settings -> Preferences. Both end up source='explicit' because that is what
-- drives injection (ComposeModeAmbient injects explicit only, and explicit
-- wins key collisions over inferred) — but they are different acts, and
-- without this distinction a kept preference is indistinguishable from a
-- typed setting, so it can neither be listed on the b-Cortex page nor undone.
-- confirmed_by is VARCHAR(255), matching created_by / updated_by / user_id on
-- this table. A UUID column would reject every actor id that is not a bare
-- UUID and fail the write outright, where the rest of the table's attribution
-- columns simply record whatever identifier the caller had.
ALTER TABLE llm_memory_preferences
    ADD COLUMN IF NOT EXISTS origin       VARCHAR(16),
    ADD COLUMN IF NOT EXISTS confirmed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS confirmed_by VARCHAR(255);

COMMENT ON COLUMN llm_memory_preferences.origin IS
    'How the row first entered: ''inferred'' when extracted from a conversation. NULL for rows authored directly by a user. Never rewritten after insert.';
COMMENT ON COLUMN llm_memory_preferences.confirmed_at IS
    'When the user accepted an inferred preference via Keep. NULL means unreviewed (source=inferred) or user-authored (origin IS NULL).';

-- No index on origin / confirmed_at: nothing queries by them. The b-Cortex
-- list reads through listByScope, which filters only on tenant / scope / user
-- / agent_module (already served by idx_user_pref_lookup) and splits inferred
-- from kept in the client. Confirm and Unconfirm are single-row updates on the
-- unique key. An index here would be write overhead against no read.

-- Backfill. Rows still sitting at source='inferred' were extracted, so their
-- origin is inferred and they remain unreviewed (confirmed_at stays NULL).
UPDATE llm_memory_preferences
   SET origin = 'inferred'
 WHERE source = 'inferred'
   AND origin IS NULL;

-- Rows already promoted by Keep before this migration cannot be identified
-- with certainty: the promotion set source='explicit' and nulled the evidence,
-- leaving them shaped exactly like a typed setting. The one distinguishing
-- trace is the key — a typed setting can only be one of the five keys the
-- Settings form writes, so a user-scoped explicit row with any other key must
-- have come from Keep. Their evidence and source_conversation_id were
-- destroyed at promotion time and cannot be recovered; only the origin is
-- restored, which is what makes them visible and reversible again.
UPDATE llm_memory_preferences
   SET origin       = 'inferred',
       confirmed_at = COALESCE(confirmed_at, updated_at)
 WHERE source = 'explicit'
   AND origin IS NULL
   AND scope = 'user'
   AND key NOT IN ('timezone', 'default_environment', 'default_namespace',
                   'manual_inputs', 'notification_channels');
