alter table "public"."notification_channel_account_mappings" rename column "channel_metadata" to "metadata";
alter table "public"."notification_channel_account_mappings" alter column "metadata" set not null;
