update billing_usage_cost set account_id = null where account_id = '00000000-0000-0000-0000-000000000000';

alter table "public"."billing_usage_cost"
  add constraint "billing_usage_cost_account_id_fkey"
  foreign key ("account_id")
  references "public"."cloud_accounts"
  ("id") on update no action on delete no action;
