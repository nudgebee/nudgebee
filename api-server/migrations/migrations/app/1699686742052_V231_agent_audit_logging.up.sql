

alter table "public"."agent_task" add column "response" jsonb
 null;

alter table "public"."agent_task" add column "agent_id" uuid
 null;

alter table "public"."agent_task" add column "source" text
 null;

alter table "public"."agent_task" add column "resoruce_id" uuid
 null;

CREATE  INDEX "agent_task_status_id_cloud_account_id_index_key" on
  "public"."agent_task" using btree ("status", "agent_id", "cloud_account_id");

alter table "public"."agent_task"
  add constraint "agent_task_agent_id_fkey"
  foreign key ("agent_id")
  references "public"."agent"
  ("id") on update restrict on delete restrict;

CREATE TABLE "public"."agent_audit_log" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamptz NOT NULL DEFAULT now(), "updated_at" Timestamp NOT NULL DEFAULT now(), "url" text NOT NULL, "client_ip" text NOT NULL, "status_code" integer NOT NULL, "headers" jsonb NOT NULL, "agent_id" uuid, "tenant_id" uuid, "cloud_account_id" uuid, PRIMARY KEY ("id") );
CREATE OR REPLACE FUNCTION "public"."set_current_timestamp_updated_at"()
RETURNS TRIGGER AS $$
DECLARE
  _new record;
BEGIN
  _new := NEW;
  _new."updated_at" = NOW();
  RETURN _new;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER "set_public_agent_audit_log_updated_at"
BEFORE UPDATE ON "public"."agent_audit_log"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_agent_audit_log_updated_at" ON "public"."agent_audit_log"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."agent_audit_log" add column "method" text
 null;

alter table "public"."agent_audit_log" add column "time_taken" integer
 null;

alter table "public"."agent_task" add column "source_id" uuid
 null;
