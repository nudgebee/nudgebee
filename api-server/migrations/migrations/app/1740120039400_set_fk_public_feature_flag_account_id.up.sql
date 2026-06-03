alter table "public"."feature_flag"
  add constraint "feature_flag_account_id_fkey"
  foreign key ("account_id")
  references "public"."cloud_accounts"
  ("id") on update restrict on delete restrict;
