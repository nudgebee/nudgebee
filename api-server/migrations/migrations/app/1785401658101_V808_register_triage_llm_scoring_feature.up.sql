-- Register LLM-assisted triage scoring as a per-tenant feature so it appears in Tenant Settings
-- and can be rolled out tenant-by-tenant (default off; checked via
-- tenant.IsFeatureExplicitlyEnabled with the TRIAGE_LLM_SCORING key).
--
-- To enable for a tenant:
--   INSERT INTO public.feature_flag (feature_id, tenant_id, status)
--   VALUES ('TRIAGE_LLM_SCORING', '<tenant-uuid>', 'enabled');
-- To disable:
--   DELETE FROM public.feature_flag
--   WHERE feature_id = 'TRIAGE_LLM_SCORING' AND tenant_id = '<tenant-uuid>';

INSERT INTO public.feature (value, description)
VALUES (
    'TRIAGE_LLM_SCORING',
    'LLM-assisted triage scoring: classify each alert into a per-class verdict and derive a real P0–P3 priority with a deterministic policy, instead of the legacy severity×environment formula. Per-tenant enrolment during rollout.'
)
ON CONFLICT (value) DO NOTHING;
