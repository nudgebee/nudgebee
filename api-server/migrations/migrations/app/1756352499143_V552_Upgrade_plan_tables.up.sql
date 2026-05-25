
CREATE TABLE "public"."upgrade_plan_status_type" ("value" text NOT NULL, "comment" text, PRIMARY KEY ("value") );

INSERT INTO "public"."upgrade_plan_status_type"("value", "comment") VALUES (E'Pending', null);

INSERT INTO "public"."upgrade_plan_status_type"("value", "comment") VALUES (E'Completed', null);

INSERT INTO "public"."upgrade_plan_status_type"("value", "comment") VALUES (E'Skipped', null);

INSERT INTO "public"."upgrade_plan_status_type"("value", "comment") VALUES (E'Failed', null);

CREATE TABLE "public"."upgrade_plan_steps" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "tenant_id" uuid NOT NULL, "account_id" uuid NOT NULL, "plan_id" uuid, "title" text NOT NULL, "sequence" integer NOT NULL, "description" text NOT NULL, "status" text NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("account_id") REFERENCES "public"."cloud_accounts"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("status") REFERENCES "public"."upgrade_plan_status_type"("value") ON UPDATE no action ON DELETE no action);
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."upgrade_plan_tasks" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "step_id" uuid NOT NULL, "title" text NOT NULL, "sequence" integer NOT NULL, "description" text NOT NULL, "status" text NOT NULL, "action" text, PRIMARY KEY ("id") , FOREIGN KEY ("step_id") REFERENCES "public"."upgrade_plan_steps"("id") ON UPDATE cascade ON DELETE cascade);
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."upgrade_plan_tasks"
  add constraint "upgrade_plan_tasks_status_fkey"
  foreign key ("status")
  references "public"."upgrade_plan_status_type"
  ("value") on update no action on delete no action;
