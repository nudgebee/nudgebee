
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_account_score" add column "updated_at" Timestamp
--  null default now();
--
-- CREATE OR REPLACE FUNCTION "public"."set_current_timestamp_updated_at"()
-- RETURNS TRIGGER AS $$
-- DECLARE
--   _new record;
-- BEGIN
--   _new := NEW;
--   _new."updated_at" = NOW();
--   RETURN _new;
-- END;
-- $$ LANGUAGE plpgsql;
-- CREATE TRIGGER "set_public_cloud_account_score_updated_at"
-- BEFORE UPDATE ON "public"."cloud_account_score"
-- FOR EACH ROW
-- EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
-- COMMENT ON TRIGGER "set_public_cloud_account_score_updated_at" ON "public"."cloud_account_score"
-- IS 'trigger to set value of column "updated_at" to current timestamp on row update';

CREATE TRIGGER "set_public_cloud_account_score_updated_at"
BEFORE UPDATE ON "public"."cloud_account_score"
FOR EACH ROW EXECUTE FUNCTION set_current_timestamp_updated_at();COMMENT ON TRIGGER "set_public_cloud_account_score_updated_at" ON "public"."cloud_account_score"
IS E'trigger to set value of column "updated_at" to current timestamp on row update';

alter table "public"."cloud_account_score" alter column "updated_at" set default now();
alter table "public"."cloud_account_score" alter column "updated_at" drop not null;
alter table "public"."cloud_account_score" add column "updated_at" time;
