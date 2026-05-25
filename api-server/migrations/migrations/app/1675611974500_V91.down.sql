
alter table "public"."cloud_accounts" drop constraint "role_access_secret_check";
alter table "public"."cloud_accounts" add constraint "role_access_secret_check" check (CHECK (assume_role IS NOT NULL OR access_key IS NOT NULL OR access_secret IS NOT NULL OR username IS NOT NULL OR password IS NOT NULL));
