

CREATE TABLE "public"."jira_configurations" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "tenant" uuid NOT NULL, "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "created_by" uuid NOT NULL, "updated_by" uuid, "name" text NOT NULL, "url" text NOT NULL, "username" text NOT NULL, "password" text NOT NULL, "auth_type" text NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("tenant") REFERENCES "public"."tenant"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE no action ON DELETE no action, FOREIGN KEY ("updated_by") REFERENCES "public"."users"("id") ON UPDATE no action ON DELETE no action);
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
CREATE TRIGGER "set_public_jira_configurations_updated_at"
BEFORE UPDATE ON "public"."jira_configurations"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_jira_configurations_updated_at" ON "public"."jira_configurations" 
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."jira_configurations" add column "status" text
 null;

alter table "public"."jira_configurations" add column "last_connected" timestamp
 null;

CREATE TABLE "public"."tickets" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "created_by" uuid NOT NULL, "tenant" uuid NOT NULL, "reference_id" uuid NOT NULL, "ticket_type" text NOT NULL, "configuration_id" uuid NOT NULL, "status" text, "message" text, "ticket_id" text NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("tenant") REFERENCES "public"."tenant"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE no action ON DELETE no action, FOREIGN KEY ("configuration_id") REFERENCES "public"."jira_configurations"("id") ON UPDATE no action ON DELETE no action);
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
CREATE TRIGGER "set_public_tickets_updated_at"
BEFORE UPDATE ON "public"."tickets"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_tickets_updated_at" ON "public"."tickets" 
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."tickets" add column "assignee" uuid
 null;

ALTER TABLE "public"."tickets" ALTER COLUMN "assignee" TYPE text;
