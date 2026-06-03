

CREATE TABLE "public"."llm_rag_audit" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "cloud_account_id" uuid NOT NULL, "module" text NOT NULL, "query" text NOT NULL, "score" float8 NOT NULL, "response" jsonb NOT NULL, PRIMARY KEY ("id") );
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."llm_rag_audit" add column "conversation_id" uuid
 null;
