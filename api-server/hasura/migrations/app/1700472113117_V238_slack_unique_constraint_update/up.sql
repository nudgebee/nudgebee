
alter table "public"."slack_user" drop constraint "slack_user_slack_user_id_deleted_at_key";
alter table "public"."slack_user" add constraint "slack_user_deleted_at_slack_user_id_slack_app_id_key" unique ("deleted_at", "slack_user_id", "slack_app_id");
