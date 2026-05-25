
alter table "public"."auto_pilot_task" add column "account_id" uuid
 null;

alter table "public"."auto_pilot_task" drop column "account_id" cascade;

CREATE TABLE IF NOT EXISTS "public"."auto_playbook" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamp NOT NULL DEFAULT now(), "update_at" timestamp NOT NULL DEFAULT now(), "created_by" uuid NOT NULL, "name" text NOT NULL, "tenant_id" uuid NOT NULL, "account_id" uuid NOT NULL, "resource_filter" jsonb NOT NULL DEFAULT jsonb_build_object(), "tasks" jsonb NOT NULL DEFAULT jsonb_build_array(), "source" jsonb NOT NULL DEFAULT jsonb_build_object(), "notification" jsonb NOT NULL DEFAULT jsonb_build_object(), "last_schedule_time" timestamp, "last_executed_time" timestamp, "execution_status" text NOT NULL, "status" Text NOT NULL, "start_at" timestamp NOT NULL DEFAULT now(), "end_at" timestamp, PRIMARY KEY ("id") , FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("account_id") REFERENCES "public"."cloud_accounts"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("id"), UNIQUE ("tenant_id", "account_id", "name"));COMMENT ON TABLE "public"."auto_playbook" IS E'meta data for auto playbook';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS "public"."auto_playbook_task" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "tenant_id" uuid NOT NULL, "auto_playbook_id" uuid NOT NULL, "status" text NOT NULL, "name" text NOT NULL, "action_id" uuid, "scheduled_time" timestamp NOT NULL DEFAULT now(), "reason" text, "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "meta_data" jsonb NOT NULL DEFAULT jsonb_build_object(), PRIMARY KEY ("id") , FOREIGN KEY ("auto_playbook_id") REFERENCES "public"."auto_playbook"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("id"));
CREATE EXTENSION IF NOT EXISTS pgcrypto;
