
alter table "public"."cloud_accounts" add constraint "account_name_check" check (length(trim(account_name)) > 3);
