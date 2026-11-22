-- Removed Teams installations are not restored; workspaces connect their teams again.
DELETE FROM "public"."integration_statuses" AS "s"
WHERE "s"."value" = 'released'
  AND NOT EXISTS (SELECT 1 FROM "public"."integrations" AS "i" WHERE "i"."status" = 'released');

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM "public"."integrations"
    WHERE "type" = 'ms_teams'
    GROUP BY "name"
    HAVING count(*) > 1
  ) THEN
    CREATE UNIQUE INDEX IF NOT EXISTS "integrations_ms_teams_name_uk"
      ON "public"."integrations" ("name")
      WHERE "type" = 'ms_teams';
  END IF;
END $$;
