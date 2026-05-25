
alter table "public"."notification_channel_account_mappings" drop constraint "notification_channel_account_mappings_platform_team_id_channel_id_key";
alter table "public"."notification_channel_account_mappings" add constraint "notification_channel_account_mappings_channel_id_key" unique ("channel_id");


alter table "public"."notification_channel_account_mappings" drop constraint "notification_channel_account_mappings_channel_id_key";

DROP TABLE "public"."notification_channel_account_mappings";
