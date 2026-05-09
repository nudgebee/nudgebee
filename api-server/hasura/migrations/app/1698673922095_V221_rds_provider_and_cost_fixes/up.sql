
INSERT INTO "public"."cloud_provider_type"("comment", "value") VALUES (E'Postgres', E'Postgres');

alter table "public"."cloud_accounts" add column "parent_account_id" uuid
 null;

alter table "public"."spends" add column "exclude_aggregate" boolean
 null default 'false';
