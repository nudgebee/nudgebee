
alter table "public"."cloud_accounts" alter column "assume_role" drop not null;

alter table "public"."cloud_accounts" add constraint "role_access_secret_check" check ((assume_role is not null) or (access_key is not null or access_secret is not null));
