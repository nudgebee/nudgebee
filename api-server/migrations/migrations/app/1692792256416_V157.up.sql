
alter table "public"."cloud_accounts" add column "etl_attempt" integer
 not null default '0';
