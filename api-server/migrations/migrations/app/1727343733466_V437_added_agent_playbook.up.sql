
CREATE TABLE "public"."agent_playbook_trigger" ("name" text NOT NULL, "params" jsonb, PRIMARY KEY ("name") , UNIQUE ("name"));

CREATE TABLE "public"."agent_playbook_action" ("name" text NOT NULL, "params" jsonb, PRIMARY KEY ("name") );

CREATE TABLE "public"."agent_playbook" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "tenant_id" uuid, "cloud_account_id" uuid, "trigger" text NOT NULL, "action" text NOT NULL, "trigger_params" jsonb, "action_params" jsonb, PRIMARY KEY ("id") );
CREATE EXTENSION IF NOT EXISTS pgcrypto;

INSERT INTO "public"."agent_playbook_trigger"("params", "name") VALUES ('{"alert_name":"text"}', E'on_prometheus_alert');
