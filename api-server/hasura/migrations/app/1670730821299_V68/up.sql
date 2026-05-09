
alter table "public"."cloud_accounts" add column "access_key" text
 null;

alter table "public"."cloud_accounts" add column "access_seceret" text
 null;

alter table "public"."cloud_accounts" rename column "access_seceret" to "access_secret";
