
CREATE TABLE "public"."resource_attrs" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "name" text NOT NULL, "value" text, "created_at" timestamptz NOT NULL DEFAULT now(), "updated_at" timestamptz NOT NULL DEFAULT now(), "resource_id" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("resource_id") REFERENCES "public"."cloud_resourses"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("id"));
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
CREATE TRIGGER "set_public_resource_attrs_updated_at"
BEFORE UPDATE ON "public"."resource_attrs"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_resource_attrs_updated_at" ON "public"."resource_attrs"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."resource_attrs" add column "type" text
 not null;

CREATE UNIQUE INDEX "resource_id_attrs_name_type" on
  "public"."resource_attrs" using btree ("resource_id", "name", "type");
