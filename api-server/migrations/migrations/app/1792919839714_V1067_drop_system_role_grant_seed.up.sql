-- Drop the grant rows V833 seeded on the built-in system roles.
--
-- V833 copied each built-in role's grant set (derived from actions.yaml by
-- app/src/lib/systemRoleGrants.ts) into custom_role_permissions, for a unified
-- resolver that would read built-in roles from these tables. That resolver was
-- never built, and nothing reads the rows: the grant resolver skips is_system
-- roles (security/custom_roles_feature.go), every custom-role query is scoped
-- to a tenant and the system roles have none, and the Roles editor derives the
-- built-in summary from actions.yaml on each request
-- (app/src/pages/api/permissions/catalog.ts). Each actions.yaml change left the
-- copy stale, so it is removed rather than regenerated. The system role rows in
-- custom_roles stay; only their grant rows go.
--
-- Touches only the few hundred system-role rows; no lock beyond the DELETE.

DELETE FROM "public"."custom_role_permissions"
WHERE "custom_role_id" IN (SELECT "id" FROM "public"."custom_roles" WHERE "is_system");
