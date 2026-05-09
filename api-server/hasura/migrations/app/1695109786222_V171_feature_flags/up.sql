

CREATE TABLE "public"."feature" ("value" text NOT NULL, "description" text, PRIMARY KEY ("value") , UNIQUE ("value"));

CREATE TABLE "public"."feature_flag" ("feature" text NOT NULL, "tenant_id" uuid NOT NULL, "status" text NOT NULL DEFAULT 'enabled', "created_at" timestamptz NOT NULL DEFAULT now(), "feature_module_id" text, PRIMARY KEY ("feature","tenant_id","feature_module_id") , FOREIGN KEY ("feature") REFERENCES "public"."feature"("value") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE restrict, CONSTRAINT "feature_flag_status" CHECK (status in ('enabled', 'disabled')));

alter table "public"."feature_flag" rename column "feature" to "feature_id";

INSERT INTO "public"."feature"("description", "value") VALUES (E'Send Weekly Emails for Spend Usage', E'WEEKLY_SPEND_EMAIL_NOTIFICATION');

CREATE EXTENSION IF NOT EXISTS pgcrypto;
alter table "public"."feature_flag" add column "id" uuid
 not null default gen_random_uuid();

BEGIN TRANSACTION;
ALTER TABLE "public"."feature_flag" DROP CONSTRAINT "feature_flag_pkey";

ALTER TABLE "public"."feature_flag"
    ADD CONSTRAINT "feature_flag_pkey" PRIMARY KEY ("id");
COMMIT TRANSACTION;

alter table "public"."feature_flag" alter column "feature_module_id" drop not null;

alter table "public"."feature_flag" add constraint "feature_flag_feature_id_tenant_id_feature_module_id_key" unique ("feature_id", "tenant_id", "feature_module_id");
