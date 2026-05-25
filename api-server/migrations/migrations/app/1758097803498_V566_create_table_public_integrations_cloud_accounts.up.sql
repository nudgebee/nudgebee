


CREATE TABLE "public"."integrations_cloud_accounts" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "integration_id" uuid NOT NULL, "cloud_account_id" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("integration_id") REFERENCES "public"."integrations"("id") ON UPDATE restrict ON DELETE cascade, FOREIGN KEY ("cloud_account_id") REFERENCES "public"."cloud_accounts"("id") ON UPDATE restrict ON DELETE restrict);
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."integrations_cloud_accounts" add column "tenant_id" uuid
 not null;

alter table "public"."integrations_cloud_accounts" add constraint "integrations_cloud_accounts_integration_id_cloud_account_id_key" unique ("integration_id", "cloud_account_id");

alter table "public"."integrations_cloud_accounts" add column "default_log_provider" boolean
 null;

alter table "public"."integrations_cloud_accounts" add column "default_traces_provider" boolean
 not null default 'false';

alter table "public"."integrations_cloud_accounts" drop column "default_log_provider" cascade;

alter table "public"."integrations_cloud_accounts" add column "default_log_provider" boolean
 null default 'false';

alter table "public"."integrations_cloud_accounts" alter column "default_log_provider" set not null;

alter table "public"."integrations_cloud_accounts" add column "default_metrics_provider" boolean
 not null default 'false';
