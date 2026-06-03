
DROP table "public"."dw_pipe_stream";

CREATE TABLE "public"."dw_pipe_usage" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "cloud_account_id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "start_at" timestamptz, "end_at" timestamptz, "credit_used" numeric, "bytes_migrated" numeric, "files_inserted" numeric, "pipe_id" integer, "pipe_name" text, PRIMARY KEY ("id") , FOREIGN KEY ("cloud_account_id") REFERENCES "public"."cloud_accounts"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("pipe_id", "pipe_name", "cloud_account_id", "tenant_id"));
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."dw_pipe" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "cloud_account_id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "pipe_id" integer, "pipe_name" text, "table_name" text, "schema_name" text, "database_name" text, "owner" text, "created_at" timestamptz, "deleted_at" timestamptz, PRIMARY KEY ("id") , FOREIGN KEY ("cloud_account_id") REFERENCES "public"."cloud_accounts"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("cloud_account_id", "tenant_id", "pipe_id", "pipe_name", "table_name", "schema_name", "database_name"));
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."dw_pipe_usage" rename column "bytes_migrated" to "bytes_inserted";

alter table "public"."dw_pipe_usage" drop constraint "dw_pipe_usage_pipe_id_pipe_name_cloud_account_id_tenant_id_key";

alter table "public"."dw_pipe_usage" add constraint "dw_pipe_usage_cloud_account_id_tenant_id_pipe_id_start_at_end_at_key" unique ("cloud_account_id", "tenant_id", "pipe_id", "start_at", "end_at");
