
CREATE TABLE "public"."upgrade_plan" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "created_by" uuid, "updated_by" uuid, "current_version" text NOT NULL, "target_version" text NOT NULL, "owner" uuid, "k8s_provider" text NOT NULL, "account_id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "status" text NOT NULL DEFAULT 'Pending', PRIMARY KEY ("id") , FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("account_id") REFERENCES "public"."cloud_accounts"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("status") REFERENCES "public"."upgrade_plan_status_type"("value") ON UPDATE restrict ON DELETE restrict);
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
CREATE TRIGGER "set_public_upgrade_plan_updated_at"
BEFORE UPDATE ON "public"."upgrade_plan"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_upgrade_plan_updated_at" ON "public"."upgrade_plan"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."upgrade_plan_steps"
  add constraint "upgrade_plan_steps_plan_id_fkey"
  foreign key ("plan_id")
  references "public"."upgrade_plan"
  ("id") on update cascade on delete cascade;

alter table "public"."upgrade_plan_steps" add column "created_by" uuid
 null;

alter table "public"."upgrade_plan_steps" add column "updated_by" uuid
 null;

alter table "public"."upgrade_plan_steps" add column "owner" uuid
 null;

alter table "public"."upgrade_plan_steps"
  add constraint "upgrade_plan_steps_created_by_fkey"
  foreign key ("created_by")
  references "public"."users"
  ("id") on update set null on delete set null;

alter table "public"."upgrade_plan_steps"
  add constraint "upgrade_plan_steps_updated_by_fkey"
  foreign key ("updated_by")
  references "public"."users"
  ("id") on update set null on delete set null;

alter table "public"."upgrade_plan_steps"
  add constraint "upgrade_plan_steps_owner_fkey"
  foreign key ("owner")
  references "public"."users"
  ("id") on update set null on delete set null;

alter table "public"."upgrade_plan"
  add constraint "upgrade_plan_created_by_fkey"
  foreign key ("created_by")
  references "public"."users"
  ("id") on update set null on delete set null;

alter table "public"."upgrade_plan"
  add constraint "upgrade_plan_updated_by_fkey"
  foreign key ("updated_by")
  references "public"."users"
  ("id") on update set null on delete set null;

alter table "public"."upgrade_plan"
  add constraint "upgrade_plan_owner_fkey"
  foreign key ("owner")
  references "public"."users"
  ("id") on update set null on delete set null;

alter table "public"."upgrade_plan_tasks" add column "created_by" uuid
 null;

alter table "public"."upgrade_plan_tasks" add column "updated_by" uuid
 null;

alter table "public"."upgrade_plan_tasks" add column "owner" uuid
 null;

alter table "public"."upgrade_plan_tasks"
  add constraint "upgrade_plan_tasks_created_by_fkey"
  foreign key ("created_by")
  references "public"."users"
  ("id") on update set null on delete set null;

alter table "public"."upgrade_plan_tasks"
  add constraint "upgrade_plan_tasks_updated_by_fkey"
  foreign key ("updated_by")
  references "public"."users"
  ("id") on update set null on delete set null;

alter table "public"."upgrade_plan_tasks"
  add constraint "upgrade_plan_tasks_owner_fkey"
  foreign key ("owner")
  references "public"."users"
  ("id") on update set null on delete set null;
