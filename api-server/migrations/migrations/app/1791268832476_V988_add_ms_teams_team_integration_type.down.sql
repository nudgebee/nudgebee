-- The ms_teams integration names stay as Microsoft tenant ids; the old
-- "<object id>.<tenant id>" values can't be reconstructed. ms_teams_team rows
-- and their type are left in place: deleting them would drop recorded installs.
DROP INDEX IF EXISTS "public"."integrations_ms_teams_name_uk";
DROP INDEX IF EXISTS "public"."integrations_ms_teams_team_name_uk";
