
alter table "public"."cloud_resourses" add constraint "cloud_resourses_arn_account_key" unique ("arn", "account");
