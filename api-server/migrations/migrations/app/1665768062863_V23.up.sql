
CREATE TABLE "public"."businessunit_funding_sources" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "created_by" uuid NOT NULL, "updated_by" uuid NOT NULL, "amount" float8 NOT NULL DEFAULT 0.0, "planned_amount" float8 NOT NULL DEFAULT 0.0, "funding_source" uuid NOT NULL, "business_unit" uuid NOT NULL, "tenant" uuid NOT NULL, "end_date" time, "start_date" timestamp NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("tenant") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("business_unit") REFERENCES "public"."business_unit"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("funding_source") REFERENCES "public"."funding_sources"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("updated_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("funding_source", "business_unit", "tenant"));
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."funding_sources" drop column "business_unit" cascade;

alter table "public"."businessunit_funding_sources" drop column "end_date" cascade;

alter table "public"."businessunit_funding_sources" add column "end_date" timestamp
 null;

alter table "public"."businessunit_funding_sources" alter column "start_date" drop not null;

alter table "public"."businessunit_funding_sources" rename to "businessunit_funding";
