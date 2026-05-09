
CREATE TABLE "public"."cloud_resource_metrics" ("timestamp" timestamp NOT NULL DEFAULT now(), "metric" text NOT NULL, "value" float8 NOT NULL, "metric_type" text NOT NULL, "tags" jsonb NOT NULL, "id" uuid NOT NULL DEFAULT gen_random_uuid(), "cloud_resource_id" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("cloud_resource_id") REFERENCES "public"."cloud_resourses"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("cloud_resource_id", "timestamp", "metric"));
CREATE EXTENSION IF NOT EXISTS pgcrypto;
