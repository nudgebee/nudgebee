
alter table "public"."cloud_resource_details" add column "created_at" timestamptz
 null default now();

alter table "public"."cloud_resource_details" add column "updated_at" timestamptz
 null default now();

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
CREATE TRIGGER "set_public_cloud_resource_details_updated_at"
BEFORE UPDATE ON "public"."cloud_resource_details"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_cloud_resource_details_updated_at" ON "public"."cloud_resource_details"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';

alter table "public"."cloud_resource_details" add column "spot_pricing" jsonb
 null;
