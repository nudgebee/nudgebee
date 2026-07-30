-- Dynamic RBAC: tenant-defined custom roles carrying (module × verb-class)
-- permission grants, additive to the built-in roles in "public"."roles".
-- A custom role is assigned to users and/or groups; the security context
-- resolves a holder's effective grants as the union across assigned roles.
-- Tenant-global (no entity scoping) — see docs/architecture-decisions.md.

-- pgcrypto for gen_random_uuid(), citext for the case-insensitive `module`
-- column below. Both are already installed by V1, so these are no-ops on any
-- database that ran the migration tree — declared anyway so this file states its
-- own requirements rather than inheriting them from a migration 800 versions back.
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE IF NOT EXISTS "public"."custom_roles" (
    "id" uuid NOT NULL DEFAULT gen_random_uuid(),
    "tenant_id" uuid NOT NULL,
    "name" text NOT NULL,
    "description" text NOT NULL DEFAULT '',
    "created_by" uuid,
    "created_at" timestamp NOT NULL DEFAULT now(),
    "updated_at" timestamp NOT NULL DEFAULT now(),
    PRIMARY KEY ("id"),
    FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE cascade,
    FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE set null,
    UNIQUE ("tenant_id", "name")
);

-- The grants for a role: one row per (module, class). `class` is one of
-- 'Read' | 'Write' | 'Execute'; `module` is the normalized action module
-- (see app/src/lib/permissionCatalog.ts — both sides must agree on the value).
CREATE TABLE IF NOT EXISTS "public"."custom_role_permissions" (
    "id" uuid NOT NULL DEFAULT gen_random_uuid(),
    "custom_role_id" uuid NOT NULL,
    "module" citext NOT NULL,
    "class" text NOT NULL,
    PRIMARY KEY ("id"),
    FOREIGN KEY ("custom_role_id") REFERENCES "public"."custom_roles"("id") ON UPDATE restrict ON DELETE cascade,
    UNIQUE ("custom_role_id", "module", "class")
);

-- Assignment of a custom role to a principal (a user or a group). A user
-- inherits a group's custom roles by being a member of the group.
CREATE TABLE IF NOT EXISTS "public"."custom_role_assignments" (
    "id" uuid NOT NULL DEFAULT gen_random_uuid(),
    "custom_role_id" uuid NOT NULL,
    "principal_type" text NOT NULL,
    "principal_id" uuid NOT NULL,
    "tenant_id" uuid NOT NULL,
    "created_by" uuid,
    "created_at" timestamp NOT NULL DEFAULT now(),
    PRIMARY KEY ("id"),
    FOREIGN KEY ("custom_role_id") REFERENCES "public"."custom_roles"("id") ON UPDATE restrict ON DELETE cascade,
    FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE cascade,
    FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE set null,
    UNIQUE ("custom_role_id", "principal_type", "principal_id")
);

CREATE INDEX IF NOT EXISTS "idx_custom_role_permissions_role"
    ON "public"."custom_role_permissions" ("custom_role_id");

CREATE INDEX IF NOT EXISTS "idx_custom_role_assignments_principal"
    ON "public"."custom_role_assignments" ("tenant_id", "principal_type", "principal_id");
