
CREATE TABLE "public"."event_duplicates" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "event_id" uuid NOT NULL, "fingerprint" text NOT NULL, "cloud_account_id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "first_event_id" uuid NOT NULL, "previous_event_id" uuid NOT NULL, "occurrence_number" integer NOT NULL, "time_since_first_seconds" integer NOT NULL, "time_since_previous_seconds" integer NOT NULL, "created_at" timestamptz NOT NULL DEFAULT now(), PRIMARY KEY ("id") , FOREIGN KEY ("event_id") REFERENCES "public"."events"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("first_event_id") REFERENCES "public"."events"("id") ON UPDATE set null ON DELETE set null, FOREIGN KEY ("previous_event_id") REFERENCES "public"."events"("id") ON UPDATE set null ON DELETE set null);
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE  INDEX "idx_event_dup_fingerprint" on
  "public"."event_duplicates" using btree ("fingerprint", "cloud_account_id");

CREATE  INDEX "idx_event_dup_first_event" on
  "public"."event_duplicates" using btree ("first_event_id");

CREATE  INDEX "idx_event_dup_created" on
  "public"."event_duplicates" using btree ("created_at");

CREATE  INDEX "idx_event_dup_occurrence" on
  "public"."event_duplicates" using btree ("occurrence_number");

ALTER TABLE "public"."event_duplicates" ALTER COLUMN "created_at" TYPE timestamp;

CREATE TABLE "public"."event_correlations" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "event_id" uuid NOT NULL, "related_event_id" uuid NOT NULL, "correlation_type" text NOT NULL, "correlation_score" numeric NOT NULL, "correlation_reason" text NOT NULL, "time_offset_minutes" integer NOT NULL, "dependency_distance" integer NOT NULL, "created_at" timestamp NOT NULL DEFAULT now(), PRIMARY KEY ("id") , FOREIGN KEY ("event_id") REFERENCES "public"."events"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("related_event_id") REFERENCES "public"."events"("id") ON UPDATE cascade ON DELETE cascade, UNIQUE ("event_id", "related_event_id"), CONSTRAINT "event_id_not_equal" CHECK (event_id != related_event_id), CONSTRAINT "event_ correlation_type" CHECK (correlation_type IN (
    'upstream_dependency', 
    'downstream_impact', 
    'same_namespace', 
    'temporal_proximity', 
    'likely_root_cause',
    'same_service'
  )), CONSTRAINT "correlation_score_check" CHECK (correlation_score >= 0.0 AND correlation_score <= 1.0));
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE  INDEX "idx_event_corr_event" on
  "public"."event_correlations" using btree ("event_id");

alter table "public"."event_duplicates" add constraint "event_duplicates_event_id_cloud_account_id_key" unique ("event_id", "cloud_account_id");

alter table "public"."event_correlations" add column "cloud_account_id" uuid
 not null;

alter table "public"."event_correlations" add column "tenant_id" uuid
 null;

alter table "public"."event_correlations" drop constraint "event_correlations_event_id_related_event_id_key";
alter table "public"."event_correlations" add constraint "event_correlations_related_event_id_event_id_cloud_account_id_key" unique ("related_event_id", "event_id", "cloud_account_id");
