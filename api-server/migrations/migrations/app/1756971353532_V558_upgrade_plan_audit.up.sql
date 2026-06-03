
CREATE TABLE "public"."upgrade_plan_audit" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamp NOT NULL DEFAULT now(), "tenant_id" uuid NOT NULL, "plan_id" uuid NOT NULL, "step_id" uuid NOT NULL, "task_id" uuid NOT NULL, "field" text NOT NULL, "action" text NOT NULL, "old_value" text, "new_value" text NOT NULL, "actioned_by" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("actioned_by") REFERENCES "public"."users"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("plan_id") REFERENCES "public"."upgrade_plan"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("step_id") REFERENCES "public"."upgrade_plan_steps"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("task_id") REFERENCES "public"."upgrade_plan_tasks"("id") ON UPDATE cascade ON DELETE cascade);
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."upgrade_plan_audit" add column "account_id" uuid
 not null;

alter table "public"."upgrade_plan_audit"
  add constraint "upgrade_plan_audit_account_id_fkey"
  foreign key ("account_id")
  references "public"."cloud_accounts"
  ("id") on update cascade on delete cascade;
