-- Custom roles are part of the product for every tenant, not a feature flag.
--
-- security.CustomRolesEnabled no longer reads a per-tenant flag: custom roles
-- apply to every tenant unless the deployment sets CUSTOM_ROLES_DISABLED=true.
-- This removes the CUSTOM_ROLES flag rows and its catalog entry, so Tenant
-- Settings no longer offers a switch that would do nothing.
--
-- Built-in roles are unaffected: custom grants only add access, and a tenant
-- with no custom role assigned authorizes exactly as before. A tenant that had
-- the flag off ('disabled' row, or no row) now has custom roles available.
--
-- A handful of rows; no lock beyond the DELETEs. Billing references are
-- removed first because they point at the catalog row (none are expected).

DELETE FROM "public"."feature_flag" WHERE "feature_id" = 'CUSTOM_ROLES';
DELETE FROM "public"."billing_feature_mapping" WHERE "feature_id" = 'CUSTOM_ROLES';
DELETE FROM "public"."billing_plan_features" WHERE "feature_id" = 'CUSTOM_ROLES';
DELETE FROM "public"."feature" WHERE "value" = 'CUSTOM_ROLES';
