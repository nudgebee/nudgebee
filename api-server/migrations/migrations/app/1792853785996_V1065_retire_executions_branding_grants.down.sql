-- Restore the executions grants on the built-in system roles exactly as V833
-- seeded them.
--
-- Partial by necessity: the .up.sql merged tenant-authored `executions` grants
-- into `workflows` and dropped `branding` grants, and neither is recorded, so
-- tenant custom roles come back without them.
INSERT INTO "public"."custom_role_permissions" ("custom_role_id", "module", "class")
SELECT cr.id, v.module, v.class
FROM "public"."custom_roles" cr
JOIN (VALUES
  ('tenant_admin', 'executions', 'Read'),
  ('tenant_admin_readonly', 'executions', 'Read'),
  ('account_admin', 'executions', 'Read'),
  ('account_admin_readonly', 'executions', 'Read'),
  ('k8s_namespace_admin', 'executions', 'Read'),
  ('k8s_namespace_admin_readonly', 'executions', 'Read')
) AS v(system_key, module, class) ON cr."system_key" = v.system_key
WHERE cr."is_system" = true
ON CONFLICT ("custom_role_id", "module", "class") DO NOTHING;
