-- Restore the featureflags/product grants on the built-in system roles exactly
-- as V833 seeded them.
--
-- Partial by necessity: step 1 of the .up.sql merged tenant-authored
-- `featureflags` grants into `tenants`, and that mapping is not recorded
-- anywhere, so a tenant custom role that held featureflags:Read before the
-- upgrade comes back holding tenants:Read only. Rolling back the code without
-- re-granting those by hand leaves those roles unable to read feature flags.
INSERT INTO "public"."custom_role_permissions" ("custom_role_id", "module", "class")
SELECT cr.id, v.module, v.class
FROM "public"."custom_roles" cr
JOIN (VALUES
  ('tenant_admin', 'featureflags', 'Read'),
  ('tenant_admin', 'featureflags', 'Write'),
  ('tenant_admin', 'product', 'Read'),
  ('tenant_admin_readonly', 'featureflags', 'Read'),
  ('tenant_admin_readonly', 'product', 'Read'),
  ('account_admin', 'featureflags', 'Read'),
  ('account_admin', 'product', 'Read'),
  ('account_admin_readonly', 'featureflags', 'Read'),
  ('account_admin_readonly', 'product', 'Read'),
  ('k8s_namespace_admin', 'featureflags', 'Read'),
  ('k8s_namespace_admin', 'product', 'Read'),
  ('k8s_namespace_admin_readonly', 'featureflags', 'Read'),
  ('k8s_namespace_admin_readonly', 'product', 'Read')
) AS v(system_key, module, class) ON cr."system_key" = v.system_key
WHERE cr."is_system" = true
ON CONFLICT ("custom_role_id", "module", "class") DO NOTHING;
