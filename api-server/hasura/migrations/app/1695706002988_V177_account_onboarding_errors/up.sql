
CREATE TABLE "public"."cloud_account_onboarding_errors" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "tenant_id" uuid NOT NULL, "created_at" timestamp without time zone NOT NULL DEFAULT now(), "created_by" uuid NOT NULL, "account_name" text NOT NULL, "config" text NOT NULL, "error_message" text, PRIMARY KEY ("id") );
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- fix migration issue
ALTER TABLE public.cloud_accounts ALTER COLUMN billing_source DROP NOT NULL;
