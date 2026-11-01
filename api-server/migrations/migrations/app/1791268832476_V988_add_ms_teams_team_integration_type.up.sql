-- A Teams team that has the NudgeBee app installed, bound to the NudgeBee tenant
-- that connected its Microsoft tenant. integrations.name is the team's Entra
-- group id, which is globally unique, so the partial unique index keeps two
-- tenants from claiming the same team.
INSERT INTO integration_types(name, category, description)
VALUES ('ms_teams_team', 'messaging', 'Microsoft Teams team with the bot installed')
ON CONFLICT (name) DO NOTHING;

CREATE UNIQUE INDEX IF NOT EXISTS "integrations_ms_teams_team_name_uk"
  ON "public"."integrations" ("name")
  WHERE "type" = 'ms_teams_team';

-- The ms_teams integration name becomes the Microsoft tenant id. Connect used to
-- store the MSAL home_account_id ("<object id>.<tenant id>", two GUIDs). At most
-- one row per (tenant, source, Microsoft tenant) is renamed, and never onto a
-- name that already exists, so integrations' (source, type, name, tenant_id)
-- key can't abort the migration. A row left in the old format still resolves:
-- readers match the tenant id as the whole name or as the part after the dot.
WITH "candidates" AS (
  SELECT DISTINCT ON ("tenant_id", "source", split_part("name", '.', 2))
    "id", split_part("name", '.', 2) AS "aad_tenant_id"
  FROM "public"."integrations"
  WHERE "type" = 'ms_teams'
    AND "name" ~ '^[0-9A-Fa-f-]{36}\.[0-9A-Fa-f-]{36}$'
  ORDER BY "tenant_id", "source", split_part("name", '.', 2), "updated_at" DESC NULLS LAST, "id"
)
UPDATE "public"."integrations" AS "i"
SET "name" = "c"."aad_tenant_id", "updated_at" = now()
FROM "candidates" AS "c"
WHERE "i"."id" = "c"."id"
  AND NOT EXISTS (
    SELECT 1 FROM "public"."integrations" AS "o"
    WHERE "o"."type" = 'ms_teams'
      AND "o"."name" = "c"."aad_tenant_id"
      AND "o"."tenant_id" IS NOT DISTINCT FROM "i"."tenant_id"
      AND "o"."source" IS NOT DISTINCT FROM "i"."source"
  );

-- One NudgeBee tenant per Microsoft tenant. If existing data already breaks
-- that, skip the index rather than failing the deploy hook; Connect also
-- refuses a second claimant in code.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM "public"."integrations"
    WHERE "type" = 'ms_teams'
    GROUP BY "name"
    HAVING count(*) > 1
  ) THEN
    RAISE NOTICE 'ms_teams integrations share a Microsoft tenant id; skipping integrations_ms_teams_name_uk';
  ELSE
    CREATE UNIQUE INDEX IF NOT EXISTS "integrations_ms_teams_name_uk"
      ON "public"."integrations" ("name")
      WHERE "type" = 'ms_teams';
  END IF;
END $$;
