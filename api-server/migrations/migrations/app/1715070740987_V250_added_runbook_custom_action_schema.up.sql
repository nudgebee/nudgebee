
create table if not exists "public"."runbook_custom_action" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "custom_action_name" text NOT NULL, "created_at" timestamp NOT NULL DEFAULT now(), "created_by" uuid NOT NULL, "library_id" uuid NOT NULL, "configs" jsonb NOT NULL DEFAULT jsonb_build_object(), "attributes" jsonb NOT NULL DEFAULT jsonb_build_object(), "base_action_configs" jsonb NOT NULL DEFAULT jsonb_build_object(), "updated_at" timestamp, "status" text NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("id"), UNIQUE ("custom_action_name", "library_id"));COMMENT ON TABLE "public"."runbook_custom_action" IS E'stores user defined custom action for runbook ';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."runbook_custom_action_library" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "library_name" text NOT NULL, "created_at" timestamp NOT NULL DEFAULT now(), "attributes" jsonb NOT NULL DEFAULT jsonb_build_object(), PRIMARY KEY ("id") , UNIQUE ("id"));COMMENT ON TABLE "public"."runbook_custom_action_library" IS E'stores library for for runbook custom actions';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."runbook_custom_action"
  add constraint "runbook_custom_action_library_id_fkey"
  foreign key ("library_id")
  references "public"."runbook_custom_action_library"
  ("id") on update restrict on delete restrict;

CREATE TABLE "public"."runbook_custom_action_status" ("key" text NOT NULL, "value" text NOT NULL, "description" text, PRIMARY KEY ("key") );COMMENT ON TABLE "public"."runbook_custom_action_status" IS E'stores enum for custom action status for enum';

alter table "public"."runbook_custom_action_status" drop constraint "runbook_custom_action_status_pkey";

alter table "public"."runbook_custom_action_status" drop column "key" cascade;

alter table "public"."runbook_custom_action_status"
    add constraint "runbook_custom_action_status_pkey"
    primary key ("value");

INSERT INTO "public"."runbook_custom_action_status"("value", "description") VALUES (E'ACTIVE', E'the custom action is active');

INSERT INTO "public"."runbook_custom_action_status"("value", "description") VALUES (E'DISABLED', E'the custom action is disabled');

alter table "public"."runbook_custom_action"
  add constraint "runbook_custom_action_status_fkey"
  foreign key ("status")
  references "public"."runbook_custom_action_status"
  ("value") on update restrict on delete restrict;
