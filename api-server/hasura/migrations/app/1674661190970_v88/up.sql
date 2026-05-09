

alter table "public"."cloud_accounts" alter column "cloud_provider" drop not null;

alter table "public"."cloud_accounts" alter column "account_number" drop not null;

alter table "public"."cloud_accounts" add column "username" Text
 null;

alter table "public"."cloud_accounts" add column "password" text
 null;
