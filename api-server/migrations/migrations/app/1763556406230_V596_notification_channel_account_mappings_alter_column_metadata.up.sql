alter table "public"."notification_channel_account_mappings" alter column "metadata" drop not null;
alter table "public"."notification_channel_account_mappings" rename column "metadata" to "channel_metadata";
