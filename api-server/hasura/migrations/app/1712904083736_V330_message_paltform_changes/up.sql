
CREATE TABLE "public"."messaging_platforms_type" ("value" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."messaging_platforms_type"("value") VALUES (E'slack');

INSERT INTO "public"."messaging_platforms_type"("value") VALUES (E'ms_teams');

INSERT INTO "public"."messaging_platforms_type"("value") VALUES (E'google_chat');

alter table "public"."messaging_platforms" add column "platform" text
 not null;

alter table "public"."messaging_platforms"
  add constraint "messaging_platforms_platform_fkey"
  foreign key ("platform")
  references "public"."messaging_platforms_type"
  ("value") on update no action on delete no action;

alter table "public"."messaging_platforms" alter column "created_by" drop not null;

alter table "public"."messaging_platforms" alter column "updated_by" drop not null;

INSERT INTO "public"."messaging_platforms" (
    "id",
    "tenant_id",
    "platform",
    "username",
    "client_id",
    "app_id",
    "team_id",
    "team_name",
    "token",
    "scopes",
    "bot_id",
    "channels"
)
SELECT
    "si"."id",
    "si"."tenant_id",
    'slack',
    "si"."user_id",
    "si"."client_id",
    "si"."app_id",
    "si"."team_id",
    "si"."team_name",
    "si"."bot_token",
    "si"."bot_scopes",
    "si"."bot_id",
    "siw"."channels"
FROM
    "slack_installations" "si"
LEFT JOIN
    "slack_channels" "siw" ON "si"."id" = "siw"."installation_id";



 INSERT INTO "public"."messaging_platforms" (
    "id",
    "tenant_id",
    "platform",
    "username",
    "client_id",
    "team_id",
    "token",
    "refresh_token",
    "channels"
)
SELECT
    "msi"."id",
    "msi"."tenant_id",
    'ms_teams',
    "msi"."username",
    "msi"."client_id",
    "msi"."team_id",
    "msi"."access_token",
    "msi"."refresh_token",
    "msiw"."channels"
FROM
    "ms_teams_installations" "msi"
LEFT JOIN
    "ms_teams_channels" "msiw" ON "msi"."id" = "msiw"."installation_id";