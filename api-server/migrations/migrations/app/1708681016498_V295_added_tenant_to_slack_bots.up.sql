
CREATE EXTENSION IF NOT EXISTS pgcrypto;
alter table "public"."slack_bots" add column "tenant_id" uuid
 not null default gen_random_uuid();

ALTER TABLE "public"."slack_bots" ALTER COLUMN "tenant_id" drop default;
