
CREATE TABLE "public"."autopilot_attributes" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "key" text NOT NULL, "value" text NOT NULL, PRIMARY KEY ("id") , UNIQUE ("key"), UNIQUE ("id"), UNIQUE ("key"));COMMENT ON TABLE "public"."autopilot_attributes" IS E'to store variables related to autopilot';
CREATE EXTENSION IF NOT EXISTS pgcrypto;
