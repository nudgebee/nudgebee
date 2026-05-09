-- Register the Memory Module as a gated feature in the existing feature catalog.
-- Per-tenant activation uses the pre-existing public.feature_flag table; no
-- new allowlist table is needed.
--
-- To enable for a tenant:
--   INSERT INTO public.feature_flag (feature_id, tenant_id, status)
--   VALUES ('MEMORY_MODULE', '<tenant-uuid>', 'enabled');
-- To disable:
--   DELETE FROM public.feature_flag
--   WHERE feature_id = 'MEMORY_MODULE' AND tenant_id = '<tenant-uuid>';

INSERT INTO public.feature (value, description)
VALUES (
    'MEMORY_MODULE',
    'Layered memory architecture for LLM agents (soul, preferences, patterns, decisions, policy, account context, session working memory, heartbeat, collective). Per-tenant enrolment during rollout.'
)
ON CONFLICT (value) DO NOTHING;
