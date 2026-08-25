-- Retire the `featureflags` and `product` permission modules.
--
-- Both stopped being grantable in app/src/lib/permissionCatalog.ts:
--   * featureflags — folded into `tenants` (MODULE_ALIASES). The two reads every
--     role has to make to render feature-gated UI now classify as tenants:Read,
--     and featureflag_upsert as tenants:Write (UpsertFeatureFlags gates on
--     CanManage("tenants","Write")).
--   * product      — product_updates_list is `tenant_agnostic: true`, reachable
--     by every signed-in user, so there is nothing left to grant.
--
-- Rows for a module no action classifies to are inert (the gateway matches on
-- the classified key), but they would keep rendering as ticked checkboxes in the
-- Roles editor's stored state and would silently drift from the seeded system
-- roles that systemRoleGrants.test.ts guards. So: migrate the featureflags
-- grants tenant custom roles actually hold onto the equivalent `tenants` grant,
-- then drop both modules everywhere.
--
-- The system roles (V833 seed) need no conversion — every one of them is already
-- seeded with tenants:Read, and the only featureflags:Write row was on
-- tenant_admin, which already holds tenants:Write.

-- 1. Carry tenant-authored grants over to `tenants` at the same class, so a
--    custom role that could read/write feature flags still can. ON CONFLICT
--    covers roles that already hold the tenants grant.
INSERT INTO "public"."custom_role_permissions" ("custom_role_id", "module", "class")
SELECT crp."custom_role_id", 'tenants', crp."class"
FROM "public"."custom_role_permissions" crp
WHERE crp."module" = 'featureflags'
ON CONFLICT ("custom_role_id", "module", "class") DO NOTHING;

-- 2. Drop the retired modules. `product` carries no replacement — the action it
--    gated is now open to every signed-in user.
DELETE FROM "public"."custom_role_permissions"
WHERE "module" IN ('featureflags', 'product');
