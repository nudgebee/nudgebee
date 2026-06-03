

CREATE TABLE "public"."llm_functions" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "account_id" uuid, "name" text NOT NULL, "description" text, "prompt" text NOT NULL, "variables" jsonb, "variable_defaults" jsonb, "status" text DEFAULT 'draft', "version" integer, "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "created_by" uuid, "updated_by" uuid, PRIMARY KEY ("id") , FOREIGN KEY ("account_id") REFERENCES "public"."cloud_accounts"("id") ON UPDATE no action ON DELETE no action, FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE no action ON DELETE no action, FOREIGN KEY ("updated_by") REFERENCES "public"."users"("id") ON UPDATE no action ON DELETE no action, UNIQUE ("id"), UNIQUE ("account_id", "name"));
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."llm_functions" add column "tenant_id" uuid
 not null;
