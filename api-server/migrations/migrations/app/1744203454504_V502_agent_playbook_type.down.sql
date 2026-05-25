
alter table "public"."agent_playbook" drop constraint "agent_playbook_source_fkey";

DELETE FROM "public"."agent_playbook_source" WHERE "value" = 'agent';

DELETE FROM "public"."agent_playbook_source" WHERE "value" = 'user';

DROP TABLE "public"."agent_playbook_source";

alter table "public"."agent_playbook" rename column "source" to "type";


-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."agent_playbook" add column "type" text
--  null default 'agent';

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."agent_playbook" add column "updated_at" timestamptz
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
-- CREATE TRIGGER "set_public_agent_playbook_updated_at"
-- BEFORE UPDATE ON "public"."agent_playbook"
-- FOR EACH ROW
-- EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
-- COMMENT ON TRIGGER "set_public_agent_playbook_updated_at" ON "public"."agent_playbook"
-- IS 'trigger to set value of column "updated_at" to current timestamp on row update';

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."agent_playbook" add column "created_at" timestamptz
--  null default now();
--
-- CREATE OR REPLACE FUNCTION "public"."set_current_timestamp_created_at"()
-- RETURNS TRIGGER AS $$
-- DECLARE
--   _new record;
-- BEGIN
--   _new := NEW;
--   _new."created_at" = NOW();
--   RETURN _new;
-- END;
-- $$ LANGUAGE plpgsql;
-- CREATE TRIGGER "set_public_agent_playbook_created_at"
-- BEFORE UPDATE ON "public"."agent_playbook"
-- FOR EACH ROW
-- EXECUTE PROCEDURE "public"."set_current_timestamp_created_at"();
-- COMMENT ON TRIGGER "set_public_agent_playbook_created_at" ON "public"."agent_playbook"
-- IS 'trigger to set value of column "created_at" to current timestamp on row update';
