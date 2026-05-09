
alter table "public"."cloud_accounts" drop column "port" cascade;

alter table "public"."cloud_accounts" drop column "password" cascade;

alter table "public"."cloud_accounts" drop column "username" cascade;

alter table "public"."cloud_accounts" drop column "account_email" cascade;

alter table "public"."cloud_accounts" add column "agent_access_key" text
 null;

alter table "public"."cloud_accounts" add column "agent_access_secret" text
 null;

alter table "public"."cloud_accounts" add column "agent_synced_at" timestamp
 null;

update cloud_accounts
set agent_access_key = access_key, agent_access_secret = access_secret
where account_type = 'kubernetes';

alter table "public"."cloud_accounts" alter column "account_type" set not null;
