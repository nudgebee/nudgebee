
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP table "public"."ms_teams_installations";

alter table "public"."ms_teams_channels"
  add constraint "ms_teams_channels_installation_id_fkey"
  foreign key ("installation_id")
  references "public"."ms_teams_installations"
  ("id") on update cascade on delete cascade;

CREATE TRIGGER "notify_hasura_audit_ms_teams_channels_INSERT"
AFTER INSERT ON "public"."ms_teams_channels"
FOR EACH ROW EXECUTE FUNCTION hdb_catalog."notify_hasura_audit_ms_teams_channels_INSERT"();

alter table "public"."ms_teams_channels"
  add constraint "ms_teams_channels_updated_by_fkey"
  foreign key ("updated_by")
  references "public"."users"
  ("id") on update no action on delete no action;

CREATE TRIGGER "notify_hasura_audit_ms_teams_channels_DELETE"
AFTER DELETE ON "public"."ms_teams_channels"
FOR EACH ROW EXECUTE FUNCTION hdb_catalog."notify_hasura_audit_ms_teams_channels_DELETE"();

CREATE TRIGGER "notify_hasura_audit_ms_teams_channels_UPDATE"
AFTER UPDATE ON "public"."ms_teams_channels"
FOR EACH ROW EXECUTE FUNCTION hdb_catalog."notify_hasura_audit_ms_teams_channels_UPDATE"();

CREATE TRIGGER "notify_hasura_audit_ms_teams_channels_INSERT"
AFTER INSERT ON "public"."ms_teams_channels"
FOR EACH ROW EXECUTE FUNCTION hdb_catalog."notify_hasura_audit_ms_teams_channels_INSERT"();

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP table "public"."slack_user";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP table "public"."slack_installations";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP table "public"."slack_channels";

alter table "public"."ms_teams_channels"
  add constraint "ms_teams_channels_created_by_fkey"
  foreign key ("created_by")
  references "public"."users"
  ("id") on update no action on delete no action;
