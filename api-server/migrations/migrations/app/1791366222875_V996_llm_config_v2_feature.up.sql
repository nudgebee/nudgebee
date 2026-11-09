-- Registers the LLM_CONFIG_V2 feature so tenant feature_flag rows can
-- reference it (featureflag_upsert validates feature_id against feature.value).
-- Internal QA gate for the provider-grouped LLM configuration screen (v2);
-- deliberately not surfaced in the Tenant Settings catalog, so category is
-- 'other' and there is no customer-facing copy to write. No tenant row is
-- created here — every tenant stays on the existing view until explicitly
-- enabled. polarity is omitted: the V900 default 'opt_in' (no row = disabled)
-- matches the UI's fail-closed read. Same post-V900 register pattern as V905.
-- Idempotent so a re-apply / out-of-band dev apply is a safe no-op.

INSERT INTO "public"."feature"("value", "display_name", "category", "description")
VALUES (
    'LLM_CONFIG_V2',
    'LLM Configuration V2',
    'other',
    'Internal: provider-grouped LLM configuration screen (Phase 1, QA only)'
)
ON CONFLICT (value) DO NOTHING;
