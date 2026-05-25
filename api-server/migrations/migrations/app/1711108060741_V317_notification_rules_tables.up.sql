
CREATE TABLE "public"."notification_rules" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamp NOT NULL, "updated_at" timestamp NOT NULL, "created_by" uuid NOT NULL, "updated_by" uuid NOT NULL, "tenant_id" uuid NOT NULL, "name" text NOT NULL, "description" text, "cluster" Text NOT NULL, "namespace" text, "workload" text, "is_supressed" bool NOT NULL DEFAULT False, "is_active" bool NOT NULL DEFAULT True, PRIMARY KEY ("id") , FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE no action ON DELETE no action, FOREIGN KEY ("updated_by") REFERENCES "public"."users"("id") ON UPDATE no action ON DELETE no action, FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE cascade ON DELETE cascade, UNIQUE ("tenant_id", "name"));
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."notification_rule_mappings" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamptz NOT NULL DEFAULT now(), "created_by" uuid NOT NULL, "updated_by" uuid NOT NULL, "tenant_id" uuid NOT NULL, "rule_id" uuid NOT NULL, "platform" text NOT NULL, "channels" json NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE no action ON DELETE no action, FOREIGN KEY ("updated_by") REFERENCES "public"."users"("id") ON UPDATE no action ON DELETE no action, FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("rule_id") REFERENCES "public"."notification_rules"("id") ON UPDATE cascade ON DELETE cascade, UNIQUE ("rule_id", "platform"));
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."notification_rules" alter column "created_at" set default now();

alter table "public"."notification_rules" alter column "updated_at" set default now();

CREATE TABLE "public"."notification_platform_types" ("value" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."notification_platform_types"("value") VALUES (E'email');

INSERT INTO "public"."notification_platform_types"("value") VALUES (E'slack');

INSERT INTO "public"."notification_platform_types"("value") VALUES (E'ms_teams');

alter table "public"."notification_rule_mappings"
  add constraint "notification_rule_mappings_platform_fkey"
  foreign key ("platform")
  references "public"."notification_platform_types"
  ("value") on update no action on delete no action;
