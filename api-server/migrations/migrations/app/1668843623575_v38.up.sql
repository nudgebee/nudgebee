

alter table "public"."cloud_accounts" add column "account_url" text
 null;


CREATE TABLE "public"."severity_enum" ("value" text NOT NULL, PRIMARY KEY ("value") );

CREATE TABLE "public"."state_enum" ("value" text NOT NULL, PRIMARY KEY ("value") );

CREATE TABLE "public"."recommendation" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "tenant_id" uuid NOT NULL, "cloud_account_id" uuid NOT NULL, "resource_id" uuid NOT NULL, "resource_name" text NOT NULL, "resource_type" text NOT NULL, "resource_group" text NOT NULL, "usage" json NOT NULL, "recommendation" json NOT NULL, "severity" text NOT NULL, "state" text NOT NULL, "region" text NOT NULL, "usage_cost" float8 NOT NULL, "estimated_savings" float8 NOT NULL, "recommendation_action" text NOT NULL, "cpu_utilization" float8 NOT NULL, "size" Text NOT NULL, "note" text NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("cloud_account_id") REFERENCES "public"."cloud_accounts"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("resource_id") REFERENCES "public"."cloud_resourses"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("severity") REFERENCES "public"."severity_enum"("value") ON UPDATE cascade ON DELETE no action, FOREIGN KEY ("state") REFERENCES "public"."state_enum"("value") ON UPDATE cascade ON DELETE no action);
CREATE EXTENSION IF NOT EXISTS pgcrypto;
