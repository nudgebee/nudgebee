
CREATE TABLE "public"."user_groups" ("id" uuid NOT NULL, "created_at" timestamptz NOT NULL DEFAULT now(), "name" text NOT NULL, "description" text NOT NULL, "owner" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("owner") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict);

CREATE TABLE "public"."funding_sources" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamptz NOT NULL DEFAULT now(), "updated_at" timestamptz DEFAULT now(), "name" text NOT NULL, "amount" money NOT NULL, "start_date" timestamptz NOT NULL, "end_date" timestamptz NOT NULL, "description" text NOT NULL, "owners" uuid NOT NULL, "user_groups" uuid, PRIMARY KEY ("id") , FOREIGN KEY ("user_groups") REFERENCES "public"."user_groups"("id") ON UPDATE restrict ON DELETE restrict);
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
CREATE TRIGGER "set_public_funding_sources_updated_at"
BEFORE UPDATE ON "public"."funding_sources"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_funding_sources_updated_at" ON "public"."funding_sources" 
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."funding_sources" add column "business_unit" uuid
 not null;

alter table "public"."funding_sources" add column "tenant" uuid
 null;

alter table "public"."funding_sources" alter column "tenant" set not null;

ALTER TABLE "public"."funding_sources" ALTER COLUMN "start_date" TYPE time;

ALTER TABLE "public"."funding_sources" ALTER COLUMN "end_date" TYPE timestamp;

ALTER TABLE "public"."funding_sources" ALTER COLUMN "created_at" TYPE timestamp;

ALTER TABLE "public"."funding_sources" ALTER COLUMN "updated_at" TYPE timestamp;

alter table "public"."funding_sources"
  add constraint "funding_sources_business_unit_fkey"
  foreign key ("business_unit")
  references "public"."business_unit"
  ("id") on update cascade on delete cascade;

alter table "public"."funding_sources"
  add constraint "funding_sources_tenant_fkey"
  foreign key ("tenant")
  references "public"."tenant"
  ("id") on update cascade on delete cascade;

alter table "public"."user_groups" add column "business_unit" uuid
 not null;

alter table "public"."user_groups" add column "tenant" uuid
 null;

alter table "public"."user_groups" alter column "tenant" set not null;

alter table "public"."user_groups"
  add constraint "user_groups_tenant_fkey"
  foreign key ("tenant")
  references "public"."tenant"
  ("id") on update cascade on delete cascade;

alter table "public"."user_groups"
  add constraint "user_groups_business_unit_fkey"
  foreign key ("business_unit")
  references "public"."business_unit"
  ("id") on update cascade on delete cascade;

CREATE TABLE "public"."spends" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "start_date" timestamp without time zone NOT NULL DEFAULT now(), "end_date" timestamp NOT NULL, "resource_group" text NOT NULL, "resource_name" text, "amount" float8 NOT NULL, "unit" text DEFAULT 'USD', "business_unit" uuid NOT NULL, "tenant" uuid NOT NULL, "cloud_account" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("tenant") REFERENCES "public"."tenant"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("business_unit") REFERENCES "public"."business_unit"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("cloud_account") REFERENCES "public"."cloud_accounts"("id") ON UPDATE cascade ON DELETE cascade);
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."spends" drop column "end_date" cascade;

alter table "public"."spends" rename column "start_date" to "date";

alter table "public"."user_groups" alter column "id" set default gen_random_uuid();
