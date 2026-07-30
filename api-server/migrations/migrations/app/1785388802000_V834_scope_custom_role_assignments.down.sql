-- Reverse V797: drop the scope columns + re-keyed uniqueness, restore the
-- original scope-less unique constraint. Safe on a fresh DB; if scoped rows
-- created duplicate (role, principal) tuples across scopes, they must be
-- de-duped before this can restore the original UNIQUE.
DROP INDEX IF EXISTS "public"."idx_cra_scope";
DROP INDEX IF EXISTS "public"."uq_cra_role_principal_scope";
ALTER TABLE "public"."custom_role_assignments"
    DROP COLUMN IF EXISTS "entity_type",
    DROP COLUMN IF EXISTS "entity_id";
ALTER TABLE "public"."custom_role_assignments"
    ADD CONSTRAINT "custom_role_assignments_custom_role_id_principal_type_princ_key"
    UNIQUE ("custom_role_id", "principal_type", "principal_id");
