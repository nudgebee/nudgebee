

CREATE TABLE "public"."project_fundings" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "project" uuid NOT NULL, "funding_source" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("project") REFERENCES "public"."projects"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("funding_source") REFERENCES "public"."funding_sources"("id") ON UPDATE restrict ON DELETE restrict);
CREATE EXTENSION IF NOT EXISTS pgcrypto;