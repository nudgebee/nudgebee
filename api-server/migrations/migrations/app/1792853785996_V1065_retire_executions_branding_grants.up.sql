-- Retire the `executions` and `branding` permission modules.
--
-- Both stopped being grantable in app/src/lib/permissionCatalog.ts:
--   * executions — folded into `workflows` (MODULE_ALIASES). executions_list and
--     executions_aggregate are workflow runs, and runbook-server has always
--     checked them against the workflows grant, so an executions:Read grant was
--     admitted by the gateway and then refused.
--   * branding   — tenant branding overrides are super-admin only
--     (ee/branding/service.go), so a grant could never be honoured.
--
-- Same shape as V860: carry tenant-authored executions grants over to
-- `workflows` at the same class, then drop both modules everywhere. The system
-- roles (V833 seed) need no conversion: every role seeded with executions:Read
-- already holds workflows:Read, and none holds a branding grant.

-- 1. executions -> workflows, keeping the class. ON CONFLICT covers roles that
--    already hold the workflows grant.
INSERT INTO "public"."custom_role_permissions" ("custom_role_id", "module", "class")
SELECT crp."custom_role_id", 'workflows', crp."class"
FROM "public"."custom_role_permissions" crp
WHERE crp."module" = 'executions'
ON CONFLICT ("custom_role_id", "module", "class") DO NOTHING;

-- 2. Drop the retired modules.
DELETE FROM "public"."custom_role_permissions"
WHERE "module" IN ('executions', 'branding');
