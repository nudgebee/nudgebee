

CREATE TABLE "public"."recommendation_resolution" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "recommendation_id" uuid NOT NULL, "type" text NOT NULL, "data" jsonb, "status" text NOT NULL, "type_reference_id" text NOT NULL, "resolver_type" text NOT NULL, "resolver_id" text NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("recommendation_id") REFERENCES "public"."recommendation"("id") ON UPDATE restrict ON DELETE restrict, CONSTRAINT "type_check" CHECK ("type" in ('PullRequest', 'Ticket', 'DeploymentChange')), CONSTRAINT "status_check" CHECK ("type" in ('InProgress', 'Failed', 'Success')), CONSTRAINT "resolver_type_check" CHECK (resolver_type in ('User', 'AutoOptimize', 'AutoRunbook')));
CREATE EXTENSION IF NOT EXISTS pgcrypto;
