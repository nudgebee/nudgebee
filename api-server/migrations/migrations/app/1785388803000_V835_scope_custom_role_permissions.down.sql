-- Reverse of V798: put the grant-level scope columns back (the scope-on-grant
-- model this migration removed). Mirrors the up migration's shape — the
-- re-keyed unique index includes the scope, so a role can hold the same
-- (module, class) for several accounts.
ALTER TABLE "public"."custom_role_permissions"
    DROP CONSTRAINT IF EXISTS "custom_role_permissions_custom_role_id_module_class_key";

ALTER TABLE "public"."custom_role_permissions"
    ADD COLUMN IF NOT EXISTS "entity_type" citext,
    ADD COLUMN IF NOT EXISTS "entity_id"   text;

CREATE UNIQUE INDEX IF NOT EXISTS "uq_crp_role_module_class_scope"
    ON "public"."custom_role_permissions"
       ("custom_role_id", "module", "class",
        COALESCE("entity_type", ''), COALESCE("entity_id", ''));
