

CREATE TABLE "public"."event_rules" ("id" uuid NOT NULL, "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "account_id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "alert" text NOT NULL, "annotations" jsonb NOT NULL, "expr" text NOT NULL, "duration" text, "labels" jsonb NOT NULL, "source" text NOT NULL, "category" text NOT NULL, "severity" text NOT NULL, "enabled" boolean NOT NULL, PRIMARY KEY ("id") , UNIQUE ("account_id", "tenant_id", "alert"));
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
CREATE TRIGGER "set_public_event_rules_updated_at"
BEFORE UPDATE ON "public"."event_rules"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_event_rules_updated_at" ON "public"."event_rules"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';

alter table "public"."event_rules" add column "created_by" uuid
 null;

alter table "public"."event_rules" add column "updated_by" uuid
 null;

alter table "public"."event_rules" add column "is_editable" boolean
 null default 'true';

CREATE TABLE "public"."event_rule_source" ("value" text NOT NULL, PRIMARY KEY ("value") , UNIQUE ("value"));

INSERT INTO "public"."event_rule_source"("value") VALUES (E'prometheus');

INSERT INTO "public"."event_rule_source"("value") VALUES (E'nudgebee');

CREATE TABLE "public"."event_rule_severity" ("value" text NOT NULL, PRIMARY KEY ("value") , UNIQUE ("value"));

INSERT INTO "public"."event_rule_severity"("value") VALUES (E'warning');

INSERT INTO "public"."event_rule_severity"("value") VALUES (E'critical');

alter table "public"."event_rules"
  add constraint "event_rules_severity_fkey"
  foreign key ("severity")
  references "public"."event_rule_severity"
  ("value") on update restrict on delete restrict;

alter table "public"."event_rules"
  add constraint "event_rules_source_fkey"
  foreign key ("source")
  references "public"."event_rule_source"
  ("value") on update restrict on delete restrict;
