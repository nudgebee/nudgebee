

CREATE TABLE "public"."anamoly_config_type" ("value" text NOT NULL, "comment" text, PRIMARY KEY ("value") );

alter table "public"."anamoly_config_type" rename to "anomaly_config_type";

INSERT INTO "public"."anomaly_config_type"("value", "comment") VALUES (E'Metric', null);

CREATE TABLE "public"."anomaly_config" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamptz NOT NULL DEFAULT now(), "updated_at" timestamptz NOT NULL DEFAULT now(), "created_by" uuid NOT NULL, "updated_by" uuid NOT NULL, "type" text NOT NULL DEFAULT 'Metric', "reference_period" text NOT NULL DEFAULT '1w', "reference_unit" text NOT NULL DEFAULT 'P99', "reference_query" text NOT NULL, "change_operator" text NOT NULL, "threshold" numeric NOT NULL, "buffer_percentage" numeric NOT NULL, "tenant_id" uuid NOT NULL, "cloud_account_id" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("type") REFERENCES "public"."anomaly_config_type"("value") ON UPDATE set default ON DELETE restrict);
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
CREATE TRIGGER "set_public_anomaly_config_updated_at"
BEFORE UPDATE ON "public"."anomaly_config"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_anomaly_config_updated_at" ON "public"."anomaly_config"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."anomaly_config" alter column "reference_query" drop not null;

alter table "public"."anomaly_config" alter column "buffer_percentage" set default '20';

alter table "public"."anomaly_config" alter column "threshold" drop not null;

CREATE TABLE "public"."anomaly_type" ("value" text NOT NULL, "comment" text, PRIMARY KEY ("value") );

INSERT INTO "public"."anomaly_type"("value", "comment") VALUES (E'Latency', null);

INSERT INTO "public"."anomaly_type"("value", "comment") VALUES (E'Memory', null);

INSERT INTO "public"."anomaly_type"("value", "comment") VALUES (E'CPU', null);

INSERT INTO "public"."anomaly_type"("value", "comment") VALUES (E'ErrorRate', null);

CREATE TABLE "public"."anomaly_change_operator" ("value" text NOT NULL, "comment" text NOT NULL, PRIMARY KEY ("value") );

alter table "public"."anomaly_change_operator" alter column "comment" drop not null;

INSERT INTO "public"."anomaly_change_operator"("value", "comment") VALUES (E'GT', null);

INSERT INTO "public"."anomaly_change_operator"("value", "comment") VALUES (E'LT', null);

INSERT INTO "public"."anomaly_change_operator"("value", "comment") VALUES (E'LTE', null);

INSERT INTO "public"."anomaly_change_operator"("value", "comment") VALUES (E'GTE', null);

INSERT INTO "public"."anomaly_type"("comment", "value") VALUES (null, E'Network');

INSERT INTO "public"."anomaly_type"("comment", "value") VALUES (null, E'APIRequest');

alter table "public"."anomaly_config" add column "anomaly_type" text
 not null;

alter table "public"."anomaly_config"
  add constraint "anomaly_config_anomaly_type_fkey"
  foreign key ("anomaly_type")
  references "public"."anomaly_type"
  ("value") on update restrict on delete restrict;

alter table "public"."anomaly_config"
  add constraint "anomaly_config_change_operator_fkey"
  foreign key ("change_operator")
  references "public"."anomaly_change_operator"
  ("value") on update restrict on delete restrict;

alter table "public"."anomaly_config" drop column "reference_query" cascade;

alter table "public"."anomaly_config" drop column "threshold" cascade;
