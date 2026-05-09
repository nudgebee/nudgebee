alter table "public"."auto_pilot_approval_policy"
  add constraint "auto_pilot_approval_policy_account_id_fkey"
  foreign key ("account_id")
  references "public"."cloud_accounts"
  ("id") on update restrict on delete restrict;
