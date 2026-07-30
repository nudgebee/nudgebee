-- V798: account scope for custom roles lives on the ASSIGNMENT, not the grant.
--
-- A custom role is a pure, unscoped bundle of (module, class) grants. Where it
-- applies is a property of the binding: "Group G holds Role R on Account A",
-- stored as custom_role_assignments.entity_type='account' / entity_id=<account>
-- (columns added in V797). The resolver (NewSecurityContext) reads scope from
-- there: NULL = tenant-global (customPermissions), 'account' = the whole role
-- applies only to that account (scopedCustomPermissions[accountId]).
--
-- Scope-on-grant was the earlier model. It was rejected because it made scope a
-- property of the ROLE, so one role could not be bound to Account A for one
-- group and Account B for another without duplicating the role. The grant-level
-- columns are removed here so the two mechanisms can't drift; the write path
-- (validatePermissions) rejects grant-level scope outright.
--
-- Written to be idempotent across both starting states, because this file
-- replaces an earlier V798 that ADDED these columns:
--   * fresh database  — the columns/index never existed; every statement is a
--                       no-op and V795's inline UNIQUE is left untouched.
--   * dev database that already ran the earlier V798 — the columns and the
--                       re-keyed index are dropped and V795's UNIQUE restored.
DROP INDEX IF EXISTS "uq_crp_role_module_class_scope";

ALTER TABLE "public"."custom_role_permissions"
    DROP COLUMN IF EXISTS "entity_type",
    DROP COLUMN IF EXISTS "entity_id";

-- Restore (role, module, class) uniqueness only when it is missing — the earlier
-- V798 dropped V795's inline constraint, but on a fresh database it is still
-- there and a bare ADD CONSTRAINT would fail with "already exists".
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = '"public"."custom_role_permissions"'::regclass
          AND conname = 'custom_role_permissions_custom_role_id_module_class_key'
    ) THEN
        ALTER TABLE "public"."custom_role_permissions"
            ADD CONSTRAINT "custom_role_permissions_custom_role_id_module_class_key"
            UNIQUE ("custom_role_id", "module", "class");
    END IF;
END $$;
