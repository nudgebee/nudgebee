
CREATE TABLE "public"."usergroup_users" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "user" uuid NOT NULL, "group" uuid NOT NULL, "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "created_by" uuid NOT NULL, "updated_by" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("group") REFERENCES "public"."user_groups"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("user") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("updated_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("user", "group"));
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."user_groups" add constraint "user_groups_tenant_business_unit_name_key" unique ("tenant", "business_unit", "name");
