CREATE TABLE "public"."slo_config" ("name" text NOT NULL, "description" text, "schedule" text NOT NULL, "created_by" text NOT NULL, "updated_by" text NOT NULL, "filter_good_query" text, "filter_bad_query" text, "threshold" numeric NOT NULL, "id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamptz NOT NULL DEFAULT now(), "updated_at" timestamptz NOT NULL DEFAULT now(), "method" text NOT NULL DEFAULT 'good_bad_ratio', "histogram_query" text NOT NULL, "cloud_account_id" uuid NOT NULL, "tenant_id" uuid NOT NULL, PRIMARY KEY ("id") );
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
CREATE TRIGGER "set_public_slo_config_updated_at"
BEFORE UPDATE ON "public"."slo_config"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_slo_config_updated_at" ON "public"."slo_config"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."slo_report" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamptz NOT NULL DEFAULT now(), "updated_at" timestamptz NOT NULL DEFAULT now(), "config_id" uuid NOT NULL, "gap" numeric NOT NULL, "error_budget_target" numeric NOT NULL, "error_budget_measurement" numeric NOT NULL, "error_budget_burn_rate" numeric NOT NULL, "error_budget_burn_rate_threshold" numeric NOT NULL, "error_budget_minutes" numeric NOT NULL, "error_budget_remaining_minutes" numeric NOT NULL, "error_minutes" numeric NOT NULL, "error_budget_consumed_ratio" numeric NOT NULL, "status" text NOT NULL, "bad_events_count" numeric, "good_events_count" numeric, "events_count" numeric, "sli_measurement" numeric NOT NULL, "tenant_id" uuid NOT NULL, "cloud_account_id" uuid NOT NULL, "workload_name" text NOT NULL, "workload_namespace" text NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("config_id") REFERENCES "public"."slo_config"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("config_id", "workload_name", "workload_namespace", "cloud_account_id", "tenant_id"));
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
CREATE TRIGGER "set_public_slo_report_updated_at"
BEFORE UPDATE ON "public"."slo_report"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_slo_report_updated_at" ON "public"."slo_report"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."slo_status" ("value" text NOT NULL, PRIMARY KEY ("value") , UNIQUE ("value"));

alter table "public"."slo_config" add column "window" numeric
 null;

alter table "public"."slo_config" alter column "schedule" drop not null;

alter table "public"."slo_config" alter column "histogram_query" drop not null;

INSERT INTO "public"."slo_status"("value") VALUES (E'FIRING');

INSERT INTO "public"."slo_status"("value") VALUES (E'OK');

alter table "public"."slo_report"
  add constraint "slo_report_status_fkey"
  foreign key ("status")
  references "public"."slo_status"
  ("value") on update restrict on delete restrict;
