
CREATE TABLE "public"."tenant" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "name" varchar(255) NOT NULL, "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp without time zone NOT NULL DEFAULT now(), "created_by" uuid NOT NULL, "updated_by" uuid NOT NULL, PRIMARY KEY ("id") );
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."user_status_types" ("value" varchar(255) NOT NULL, "comment" varchar(255), PRIMARY KEY ("value") );

ALTER TABLE "public"."user_status_types" ALTER COLUMN "value" TYPE text;

ALTER TABLE "public"."user_status_types" ALTER COLUMN "comment" TYPE text;

INSERT INTO "public"."user_status_types"("value", "comment") VALUES (E'active', null);

INSERT INTO "public"."user_status_types"("value", "comment") VALUES (E'inactive', null);

INSERT INTO "public"."user_status_types"("value", "comment") VALUES (E'suspended', null);

CREATE TABLE "public"."users" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "username" varchar(255) NOT NULL, "display_name" varchar(255) NOT NULL, "status" text NOT NULL DEFAULT 'inactive', "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "created_by" uuid, "updated_by" uuid, PRIMARY KEY ("id") );
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."users"
  add constraint "users_status_fkey"
  foreign key ("status")
  references "public"."user_status_types"
  ("value") on update restrict on delete restrict;

alter table "public"."users"
  add constraint "users_created_by_fkey"
  foreign key ("created_by")
  references "public"."users"
  ("id") on update restrict on delete restrict;

alter table "public"."users"
  add constraint "users_updated_by_fkey"
  foreign key ("updated_by")
  references "public"."users"
  ("id") on update restrict on delete restrict;

alter table "public"."tenant"
  add constraint "tenant_created_by_fkey"
  foreign key ("created_by")
  references "public"."users"
  ("id") on update restrict on delete restrict;

alter table "public"."tenant"
  add constraint "tenant_updated_by_fkey"
  foreign key ("updated_by")
  references "public"."users"
  ("id") on update restrict on delete restrict;

CREATE TABLE "public"."business_unit" ("id" uuid NOT NULL, "name" varchar(255) NOT NULL, "description" varchar(255) NOT NULL, "created_at" time NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "created_by" UUID NOT NULL, "updated_by" uuid NOT NULL, "tenant" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("tenant") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("updated_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict);

CREATE TABLE "public"."projects" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "name" varchar(255) NOT NULL, "description" varchar(255) NOT NULL, "created_by" uuid NOT NULL, "updated_by" uuid NOT NULL, "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "tenant" uuid NOT NULL, "business_unit" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("business_unit") REFERENCES "public"."business_unit"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("tenant") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("updated_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict);
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."user_attrs" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "name" varchar(255) NOT NULL, "value" text, "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "user" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("user") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict);
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."businessunit_users" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "user" uuid NOT NULL, "business_unit" uuid NOT NULL, "created_at" time NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "created_by" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("user") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("business_unit") REFERENCES "public"."business_unit"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("user", "business_unit"));
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."project_users" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "user" uuid NOT NULL, "project" uuid NOT NULL, "created_at" time NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "created_by" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("user") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("project") REFERENCES "public"."projects"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("user", "project"));
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."auth_provider_types" ("value" text NOT NULL, "comment" text, PRIMARY KEY ("value") );

INSERT INTO "public"."auth_provider_types"("value", "comment") VALUES (E'oauth', null);

INSERT INTO "public"."auth_provider_types"("value", "comment") VALUES (E'credentials', null);

INSERT INTO "public"."auth_provider_types"("value", "comment") VALUES (E'email', null);

CREATE TABLE "public"."auth_types" ("value" text NOT NULL, "comment" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."auth_types"("value", "comment") VALUES (E'google', E'');

INSERT INTO "public"."auth_types"("value", "comment") VALUES (E'token', E'');

INSERT INTO "public"."auth_types"("value", "comment") VALUES (E'email', E'');

CREATE TABLE "public"."user_auths" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "credential" text NOT NULL, "provider_type" text NOT NULL, "user" uuid NOT NULL, "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "name" varchar(255), "expires_at" timestamp NOT NULL, "status" text, "account_id" varchar(255) NOT NULL, "provider" text NOT NULL, "avatar" text, PRIMARY KEY ("id") , FOREIGN KEY ("provider_type") REFERENCES "public"."auth_provider_types"("value") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("provider") REFERENCES "public"."auth_types"("value") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("user") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("status") REFERENCES "public"."user_status_types"("value") ON UPDATE restrict ON DELETE restrict, UNIQUE ("provider", "account_id"), UNIQUE ("provider", "credential", "user"), UNIQUE ("user", "name", "provider"));
CREATE EXTENSION IF NOT EXISTS pgcrypto;
