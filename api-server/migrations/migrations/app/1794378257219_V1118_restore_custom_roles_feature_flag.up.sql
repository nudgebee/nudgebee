-- Custom roles are opt-in per tenant again: security.CustomRolesEnabled reads
-- the tenant's CUSTOM_ROLES feature_flag row once more (hotfix reversing
-- V1069). This restores the catalog entry V1069 deleted, so Tenant Settings
-- offers the switch again, and turns the flag on where custom roles are in use
-- so no role that applies today stops applying:
--   1. every tenant that has a custom role, a person's or a Worker Bee's;
--   2. every tenant with Worker Bees on: a bee's rights are a custom role, so
--      creating one is refused while CUSTOM_ROLES is off;
--   3. on prod, the tenants whose flag V1069 deleted. These four had it enabled
--      (per their TENANT_UPDATE audit events) but no role yet. The ids match
--      no tenant anywhere else, so the statement does nothing there.
-- Every other tenant goes back to built-in roles only until a tenant admin
-- turns the flag on.
--
-- A handful of rows; no lock beyond the INSERTs.

INSERT INTO "public"."feature" ("value", "description", "display_name", "category", "polarity", "stored_value_inverted")
VALUES ('CUSTOM_ROLES',
        'Lets you create your own roles with exactly the permissions you choose, on top of the built-in ones. Off means only the built-in roles apply, and anyone relying on a custom role loses the extra access it gave them. Only a company admin can change this.',
        'Custom roles', 'access', 'opt_in', false)
ON CONFLICT ("value") DO NOTHING;

INSERT INTO "public"."feature_flag" ("feature_id", "tenant_id", "status")
SELECT DISTINCT 'CUSTOM_ROLES', r."tenant_id", 'enabled'
FROM "public"."custom_roles" r
WHERE NOT r."is_system" AND r."tenant_id" IS NOT NULL
ON CONFLICT ("tenant_id", "feature_id") DO NOTHING;

INSERT INTO "public"."feature_flag" ("feature_id", "tenant_id", "status")
SELECT 'CUSTOM_ROLES', f."tenant_id", 'enabled'
FROM "public"."feature_flag" f
WHERE f."feature_id" = 'WORKER_BEES' AND f."status" = 'enabled' AND f."account_id" IS NULL
ON CONFLICT ("tenant_id", "feature_id") DO NOTHING;

INSERT INTO "public"."feature_flag" ("feature_id", "tenant_id", "status")
SELECT 'CUSTOM_ROLES', t."id", 'enabled'
FROM "public"."tenant" t
WHERE t."id" IN ('8c4c578d-97ae-4787-9957-2b4c906851a3',
                 'dead155b-06cb-424c-9c1d-3ee5818ca918',
                 '8f1604fb-a45b-46b0-820e-dcbb992a45fc',
                 'e5f0b01a-f257-45c5-879e-610e99a8cc9b')
ON CONFLICT ("tenant_id", "feature_id") DO NOTHING;
