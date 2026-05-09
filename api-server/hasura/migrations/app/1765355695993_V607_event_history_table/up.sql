
CREATE TABLE "public"."event_history" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "changed_at" timestamptz NOT NULL DEFAULT now(), "changed_by" uuid, "change_type" text NOT NULL, "old_value" jsonb, "new_value" jsonb NOT NULL, "change_reason" text NOT NULL, "metadata" jsonb, "tenant_id" uuid NOT NULL, "cloud_account_id" uuid NOT NULL, "event_id" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("event_id") REFERENCES "public"."events"("id") ON UPDATE cascade ON DELETE cascade);
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE  INDEX "idx_event_history_event" on
  "public"."event_history" using btree ("changed_at");

CREATE  INDEX "idx_event_history_tenant" on
  "public"."event_history" using btree ("tenant_id", "changed_at");

CREATE  INDEX "idx_event_history_type" on
  "public"."event_history" using btree ("change_type");

DROP INDEX idx_event_history_event;
CREATE INDEX idx_event_history_event ON event_history(event_id, changed_at DESC);

DROP INDEX idx_event_history_event;
CREATE INDEX idx_event_history_event ON event_history(event_id, changed_at DESC);

ALTER TABLE event_history ADD CONSTRAINT event_history_change_type_check 
CHECK (change_type IN ('priority', 'status', 'urgency', 'evidences', 'labels', 'ends_at', 'updated_at'));
