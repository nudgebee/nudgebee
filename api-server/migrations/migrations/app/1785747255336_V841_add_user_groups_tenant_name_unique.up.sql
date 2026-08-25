-- The KG's ownership_enricher now keys NudgebeeGroup nodes on (tenant, name)
-- instead of the group's id, so two groups sharing a name within a tenant
-- would collide into the same KG node. CreateUserGroup has never enforced
-- name uniqueness at the DB level (only a frontend pre-check via
-- usergroups_check_name_exists), so a handful of pre-existing duplicates can
-- exist. Disambiguate every duplicate but the oldest (by created_at) before
-- adding the constraint, so this applies cleanly regardless of which
-- environment already has stale dupes.
WITH ranked AS (
  SELECT id, ROW_NUMBER() OVER (PARTITION BY tenant, name ORDER BY created_at, id) AS rn
  FROM user_groups
)
UPDATE user_groups ug
SET name = ug.name || ' (' || ranked.rn || ')'
FROM ranked
WHERE ug.id = ranked.id AND ranked.rn > 1;

CREATE UNIQUE INDEX IF NOT EXISTS user_groups_tenant_name_key ON user_groups (tenant, name);
