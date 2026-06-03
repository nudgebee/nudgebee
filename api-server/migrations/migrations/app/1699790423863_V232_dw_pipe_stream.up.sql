
CREATE TABLE "public"."dw_pipe_stream" ("id" uuid DEFAULT gen_random_uuid(), "cloud_account_id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "name" text, "table_name" text, "schema_name" text, "database_name" text, "start_at" timestamptz, "bytes_inserted" numeric, "credit_used" numeric, "pipe_type" text NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("cloud_account_id") REFERENCES "public"."cloud_accounts"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE restrict);
CREATE EXTENSION IF NOT EXISTS pgcrypto;
