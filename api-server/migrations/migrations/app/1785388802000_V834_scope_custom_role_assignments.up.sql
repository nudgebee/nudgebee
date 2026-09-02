-- V797: account / namespace scope for custom-role assignments (additive).
--
-- entity_type/entity_id mirror user_roles: NULL = tenant-global (the existing
-- behavior, unchanged for every current assignment), 'account' / 'k8s_namespace'
-- scope a custom-role assignment to a specific account/namespace. The resolver
-- reads these to build account-scoped custom permissions (HasScopedPermission);
-- tenant-global rows apply to every account. Re-key uniqueness to include scope
-- so a principal can hold the same custom role on multiple accounts.
ALTER TABLE "public"."custom_role_assignments"
    ADD COLUMN IF NOT EXISTS "entity_type" citext,
    ADD COLUMN IF NOT EXISTS "entity_id"   text;

-- Drop the old scope-less unique constraint (from V795) and re-key with scope.
ALTER TABLE "public"."custom_role_assignments"
    DROP CONSTRAINT IF EXISTS "custom_role_assignments_custom_role_id_principal_type_princ_key";
CREATE UNIQUE INDEX IF NOT EXISTS "uq_cra_role_principal_scope"
    ON "public"."custom_role_assignments"
       ("custom_role_id", "principal_type", "principal_id",
        COALESCE("entity_type", ''), COALESCE("entity_id", ''));
CREATE INDEX IF NOT EXISTS "idx_cra_scope"
    ON "public"."custom_role_assignments" ("tenant_id", "entity_type", "entity_id");
