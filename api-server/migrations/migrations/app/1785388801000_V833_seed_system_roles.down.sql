-- Reverse V796. The legacy user_roles / group_roles tables are never touched by
-- the up migration, so this only unwinds the additive system-role layer.

-- 1. Remove seeded system roles + their grants/assignments (explicit + order-safe).
DELETE FROM "public"."custom_role_permissions"
    WHERE "custom_role_id" IN (SELECT "id" FROM "public"."custom_roles" WHERE "is_system");
DELETE FROM "public"."custom_role_assignments"
    WHERE "custom_role_id" IN (SELECT "id" FROM "public"."custom_roles" WHERE "is_system");
DELETE FROM "public"."custom_roles" WHERE "is_system";

-- 2. Revert custom_roles system columns. All NULL-tenant system rows were
--    deleted above, so restoring NOT NULL on tenant_id is safe.
DROP INDEX IF EXISTS "public"."uq_custom_roles_system_key";
ALTER TABLE "public"."custom_roles"
    DROP COLUMN IF EXISTS "system_key",
    DROP COLUMN IF EXISTS "is_privilege_admin",
    DROP COLUMN IF EXISTS "is_system";
ALTER TABLE "public"."custom_roles" ALTER COLUMN "tenant_id" SET NOT NULL;
