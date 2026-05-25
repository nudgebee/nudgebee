
alter table "public"."cloud_accounts" drop constraint "role_access_secret_check";

alter table "public"."cloud_accounts" alter column "assume_role" set not null;
