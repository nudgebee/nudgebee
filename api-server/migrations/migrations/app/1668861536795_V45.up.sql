
alter table "public"."project_accounts" drop constraint "project_accounts_pkey";

alter table "public"."project_accounts" drop column "id" cascade;

CREATE EXTENSION IF NOT EXISTS pgcrypto;
alter table "public"."project_accounts" add column "id" uuid
 not null default gen_random_uuid();

alter table "public"."project_accounts"
    add constraint "project_accounts_pkey"
    primary key ("id");
