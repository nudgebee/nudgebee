

CREATE TABLE "public"."event_severity" ("value" text NOT NULL, PRIMARY KEY ("value") , UNIQUE ("value"));

INSERT INTO "public"."event_severity"("value") VALUES (E'DEBUG');

INSERT INTO "public"."event_severity"("value") VALUES (E'INFO');

INSERT INTO "public"."event_severity"("value") VALUES (E'LOW');

INSERT INTO "public"."event_severity"("value") VALUES (E'MEDIUM');

INSERT INTO "public"."event_severity"("value") VALUES (E'HIGH');

CREATE TABLE "public"."event_status" ("value" text NOT NULL, PRIMARY KEY ("value") , UNIQUE ("value"));

INSERT INTO "public"."event_status"("value") VALUES (E'FIRING');

INSERT INTO "public"."event_status"("value") VALUES (E'RESOLVED');

CREATE TABLE "public"."events" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamptz NOT NULL DEFAULT now(), "updated_at" timestamptz NOT NULL DEFAULT now(), "finding_id" text NOT NULL, "title" text NOT NULL, "description" text NOT NULL, "source" text NOT NULL, "aggregation_key" text NOT NULL, "failure" text NOT NULL, "finding_type" text NOT NULL, "category" text NOT NULL, "priority" text NOT NULL, "subject_type" text NOT NULL, "subject_name" text NOT NULL, "subject_namespace" text NOT NULL, "subject_node" text NOT NULL, "service_key" text NOT NULL, "cluster" text NOT NULL, "ends_at" timestamptz NOT NULL, "starts_at" timestamptz NOT NULL, "fingerprint" text NOT NULL, "evidences" jsonb NOT NULL, "tenant" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("tenant") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("id"));
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
CREATE TRIGGER "set_public_events_updated_at"
BEFORE UPDATE ON "public"."events"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_events_updated_at" ON "public"."events"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."events"
  add constraint "events_priority_fkey"
  foreign key ("priority")
  references "public"."event_severity"
  ("value") on update restrict on delete restrict;

CREATE TABLE "public"."event_source" ("value" text NOT NULL, PRIMARY KEY ("value") , UNIQUE ("value"));

INSERT INTO "public"."event_source"("value") VALUES (E'kubernetes_api_server');

INSERT INTO "public"."event_source"("value") VALUES (E'prometheus');

INSERT INTO "public"."event_source"("value") VALUES (E'manual');

INSERT INTO "public"."event_source"("value") VALUES (E'helm_release');

INSERT INTO "public"."event_source"("value") VALUES (E'callback');

INSERT INTO "public"."event_source"("value") VALUES (E'scheduler');

alter table "public"."events"
  add constraint "events_source_fkey"
  foreign key ("source")
  references "public"."event_source"
  ("value") on update restrict on delete restrict;

CREATE UNIQUE INDEX "events_id_findingid_tenant" on
  "public"."events" using btree ("tenant", "id", "finding_id");

ALTER TABLE "public"."events" ALTER COLUMN "ends_at" TYPE timestamp;

ALTER TABLE "public"."events" ALTER COLUMN "starts_at" TYPE timestamp;

ALTER TABLE "public"."events" ALTER COLUMN "created_at" TYPE timestamp;

ALTER TABLE "public"."events" ALTER COLUMN "updated_at" TYPE timestamp;


alter table "public"."events" add column "cloud_account_id" uuid
 null;

alter table "public"."events"
  add constraint "events_cloud_account_id_fkey"
  foreign key ("cloud_account_id")
  references "public"."cloud_accounts"
  ("id") on update restrict on delete restrict;

CREATE  INDEX "event_tenant_cloud_account_id" on
  "public"."events" using btree ("tenant", "cloud_account_id");

CREATE TABLE "public"."agent" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "tenant" uuid NOT NULL, "cloud_account_id" uuid NOT NULL, "type" text, "status" text NOT NULL, "last_connected_at" timestamp, "access_key" text, "access_secret" text NOT NULL, "status_message" text, PRIMARY KEY ("id") , FOREIGN KEY ("tenant") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("cloud_account_id") REFERENCES "public"."cloud_accounts"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("id"));
CREATE EXTENSION IF NOT EXISTS pgcrypto;


alter table "public"."events" alter column "category" drop not null;

alter table "public"."events" alter column "description" drop not null;

alter table "public"."events" alter column "ends_at" drop not null;

alter table "public"."events" alter column "starts_at" drop not null;

alter table "public"."events" alter column "failure" drop not null;
