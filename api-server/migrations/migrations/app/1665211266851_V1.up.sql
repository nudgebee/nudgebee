
alter table "public"."business_unit" alter column "id" set default gen_random_uuid();

CREATE TABLE "public"."tenant_users" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "tenant" uuid NOT NULL, "user" uuid NOT NULL, "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "created_by" uuid NOT NULL, "updated_by" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("tenant") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("user") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("updated_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("tenant", "user"));
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."businessunit_users" add column "tenant_user" uuid
 null;

alter table "public"."businessunit_users"
  add constraint "businessunit_users_tenant_user_fkey"
  foreign key ("tenant_user")
  references "public"."tenant_users"
  ("id") on update restrict on delete restrict;

alter table "public"."tenant" add constraint "tenant_name_key" unique ("name");

alter table "public"."businessunit_users" alter column "tenant_user" set not null;

alter table "public"."business_unit" add constraint "business_unit_name_tenant_key" unique ("name", "tenant");

alter table "public"."projects" add constraint "projects_business_unit_name_key" unique ("business_unit", "name");

CREATE EXTENSION IF NOT EXISTS citext;

ALTER TABLE "public"."business_unit" ALTER COLUMN "name" TYPE citext;

ALTER TABLE "public"."projects" ALTER COLUMN "name" TYPE citext;

ALTER TABLE "public"."tenant" ALTER COLUMN "name" TYPE citext;

ALTER TABLE "public"."users" ALTER COLUMN "username" TYPE citext;
