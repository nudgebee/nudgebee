
alter table "public"."ms_teams_channels" drop constraint "ms_teams_channels_created_by_fkey";

DROP table "public"."slack_channels";

DROP table "public"."slack_installations";

DROP table "public"."slack_user";

DROP TRIGGER IF EXISTS "notify_hasura_audit_ms_teams_channels_INSERT" ON "public"."ms_teams_channels";

DROP TRIGGER IF EXISTS "notify_hasura_audit_ms_teams_channels_UPDATE" ON "public"."ms_teams_channels";

DROP TRIGGER IF EXISTS "notify_hasura_audit_ms_teams_channels_DELETE" ON "public"."ms_teams_channels";

alter table "public"."ms_teams_channels" drop constraint "ms_teams_channels_updated_by_fkey";

alter table "public"."ms_teams_channels" drop constraint "ms_teams_channels_installation_id_fkey";

DROP table "public"."ms_teams_installations";
