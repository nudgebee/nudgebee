

CREATE TABLE "public"."system_playbook" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "playbook_name" text NOT NULL, "created_at" date NOT NULL DEFAULT now(), "account_type" text NOT NULL DEFAULT 'K8s', "tasks" jsonb NOT NULL, "trigger" jsonb NOT NULL, "attributes" jsonb NOT NULL, PRIMARY KEY ("id") , UNIQUE ("id"), UNIQUE ("playbook_name"));COMMENT ON TABLE "public"."system_playbook" IS E'all the auto mated playbooks for system use';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."system_playbook" add column "enabled" boolean
 null default 'true';

alter table "public"."system_playbook" alter column "enabled" set not null;

alter table "public"."runbook_action" add column "is_system_action" boolean
 not null default 'false';
