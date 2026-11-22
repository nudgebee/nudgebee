-- Microsoft Teams teams are bound one by one to a workspace, so one Microsoft
-- tenant can serve several workspaces. A team row is bound only while its status
-- is 'enabled'; 'released' marks a team the app left. A workspace's ms_teams row
-- exists only while it has a bound team.
--
-- Every existing Teams installation is removed: workspaces connect each team again
-- from the link the bot posts when it is @mentioned. Channel mappings and rules
-- are kept; they apply again once the same workspace reconnects the same team.
--
-- Rows removed; run before deploying and compare after:
--   SELECT type, count(*) FROM integrations WHERE type IN ('ms_teams', 'ms_teams_team') GROUP BY type;
--   SELECT count(*) FROM messaging_platforms WHERE platform = 'ms_teams';
--
-- Expected lock window: one transaction over a few hundred rows at most, seconds.
-- The DROP INDEX is metadata only.

INSERT INTO "public"."integration_statuses" ("value") VALUES ('released')
ON CONFLICT ("value") DO NOTHING;

-- Several workspaces may now hold an ms_teams row for one Microsoft tenant.
DROP INDEX IF EXISTS "public"."integrations_ms_teams_name_uk";

-- Rows another table still points at (tickets, incoming webhooks: never Teams in
-- practice) are skipped rather than failing the migration.
DELETE FROM "public"."integration_config_values" AS "v"
USING "public"."integrations" AS "i"
WHERE "v"."integration_id" = "i"."id"
  AND "i"."type" IN ('ms_teams', 'ms_teams_team')
  AND NOT EXISTS (SELECT 1 FROM "public"."tickets" AS "t" WHERE "t"."integration_id" = "i"."id")
  AND NOT EXISTS (SELECT 1 FROM "public"."event_incoming_webhooks" AS "w" WHERE "w"."integration_id" = "i"."id");

DELETE FROM "public"."integrations" AS "i"
WHERE "i"."type" IN ('ms_teams', 'ms_teams_team')
  AND NOT EXISTS (SELECT 1 FROM "public"."tickets" AS "t" WHERE "t"."integration_id" = "i"."id")
  AND NOT EXISTS (SELECT 1 FROM "public"."event_incoming_webhooks" AS "w" WHERE "w"."integration_id" = "i"."id");

DELETE FROM "public"."messaging_platforms"
WHERE "platform" = 'ms_teams';
