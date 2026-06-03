
CREATE TABLE "public"."applications_grouping" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "group_name" text NOT NULL, "description" text, "tenant_id" uuid NOT NULL, "created_at" time NOT NULL DEFAULT now(), "updated_at" time NOT NULL DEFAULT now(), "created_by" uuid NOT NULL, "updated_by" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("id"), UNIQUE ("group_name"));
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."applications_grouping" alter column "created_at" drop not null;

alter table "public"."applications_grouping" alter column "updated_at" drop not null;

alter table "public"."applications_grouping" alter column "created_by" drop not null;

alter table "public"."applications_grouping" alter column "updated_by" drop not null;
