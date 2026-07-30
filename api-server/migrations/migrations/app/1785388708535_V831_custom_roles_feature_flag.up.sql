-- Register dynamic-RBAC custom roles as a gated feature in the existing feature
-- catalog. Per-tenant activation uses the pre-existing public.feature_flag
-- table; no new allowlist table is needed.
--
-- Registering the feature does NOT enable it. With no feature_flag row,
-- security.CustomRolesEnabled() returns false and every custom-grant gate is
-- inert: NewSecurityContext skips the custom_role_* resolver entirely and
-- authorization is bit-for-bit the built-in-role behavior. That is the intended
-- default — the feature is opt-in per tenant.
--
-- To enable for a tenant:
--   INSERT INTO public.feature_flag (feature_id, tenant_id, status)
--   VALUES ('CUSTOM_ROLES', '<tenant-uuid>', 'enabled');
-- To turn it back off (grants stay in the tables, they just stop applying):
--   DELETE FROM public.feature_flag
--   WHERE feature_id = 'CUSTOM_ROLES' AND tenant_id = '<tenant-uuid>';
--
-- Deployment-wide kill switch (beats the per-tenant rows, needs no DB access):
--   CUSTOM_ROLES_DISABLED=true on the services-server pod.

INSERT INTO public.feature (value, description)
VALUES (
    'CUSTOM_ROLES',
    'Dynamic RBAC: tenant-defined custom roles carrying (module x Read/Write/Execute) permission grants, additive to the built-in roles. Per-tenant enrolment during rollout.'
)
ON CONFLICT (value) DO NOTHING;
