
CREATE TABLE "public"."auto_optimize_resource_map" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "resource_identifier" jsonb NOT NULL, "auto_optimize_type" text NOT NULL, "auto_optimize_id" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("auto_optimize_id") REFERENCES "public"."auto_pilot"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("id"));COMMENT ON TABLE "public"."auto_optimize_resource_map" IS E'resource to auto optimize mapping';
CREATE EXTENSION IF NOT EXISTS pgcrypto;
