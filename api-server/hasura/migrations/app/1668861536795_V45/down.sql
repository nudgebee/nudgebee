
alter table "public"."project_accounts" drop constraint "project_accounts_pkey";

alter table "public"."project_accounts" drop column "id" cascade
alter table "public"."project_accounts" drop column "id";
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."project_accounts" alter column "id" set default nextval('project_accounts_id_seq'::regclass);
alter table "public"."project_accounts" alter column "id" drop not null;
alter table "public"."project_accounts" add column "id" int8;

alter table "public"."project_accounts"
    add constraint "project_accounts_pkey"
    primary key ("id");
