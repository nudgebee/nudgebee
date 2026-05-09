
CREATE TABLE "public"."alert_rule" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "title" citext NOT NULL, "description" text NOT NULL, "status" text NOT NULL DEFAULT 'active', "label" jsonb NOT NULL DEFAULT '{}', "source" text NOT NULL, "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "created_by" uuid NOT NULL, "updated_by" uuid NOT NULL, "rule" jsonb NOT NULL, "tenant" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("tenant") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("tenant", "title"));
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."alert_rule_status" ("value" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."alert_rule_status"("value") VALUES (E'active');

INSERT INTO "public"."alert_rule_status"("value") VALUES (E'suspended');

alter table "public"."alert_rule"
  add constraint "alert_rule_status_fkey"
  foreign key ("status")
  references "public"."alert_rule_status"
  ("value") on update restrict on delete restrict;

CREATE TABLE "public"."alert_rule_source" ("value" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."alert_rule_source"("value") VALUES (E'spend');

INSERT INTO "public"."alert_rule_source"("value") VALUES (E'matrices');

alter table "public"."alert_rule"
  add constraint "alert_rule_source_fkey"
  foreign key ("source")
  references "public"."alert_rule_source"
  ("value") on update restrict on delete restrict;

CREATE TABLE "public"."alert_status" ("value" text NOT NULL, PRIMARY KEY ("value") );

alter table "public"."alert_status" rename to "alert_rule_state";

INSERT INTO "public"."alert_rule_state"("value") VALUES (E'firing');

INSERT INTO "public"."alert_rule_state"("value") VALUES (E'ok');

INSERT INTO "public"."alert_rule_state"("value") VALUES (E'evaluating');

alter table "public"."alert_rule" add column "state" text
 not null default 'ok';

alter table "public"."alert_rule"
  add constraint "alert_rule_state_fkey"
  foreign key ("state")
  references "public"."alert_rule_state"
  ("value") on update restrict on delete restrict;

alter table "public"."alert_rule"
  add constraint "alert_rule_created_by_fkey"
  foreign key ("created_by")
  references "public"."users"
  ("id") on update restrict on delete restrict;

alter table "public"."alert_rule"
  add constraint "alert_rule_updated_by_fkey"
  foreign key ("updated_by")
  references "public"."users"
  ("id") on update restrict on delete restrict;

alter table "public"."alert_rule" add column "evaluate_at" text
 not null;

alter table "public"."alert_rule" rename to "alert_rules";

CREATE TABLE "public"."alert_history" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "alert_rule_id" uuid NOT NULL, "created_at" timestamp without time zone NOT NULL, "updated_at" timestamp NOT NULL, "data" jsonb, "state" text NOT NULL DEFAULT 'ok', PRIMARY KEY ("id") , FOREIGN KEY ("alert_rule_id") REFERENCES "public"."alert_rules"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("state") REFERENCES "public"."alert_rule_state"("value") ON UPDATE restrict ON DELETE restrict);
CREATE EXTENSION IF NOT EXISTS pgcrypto;
