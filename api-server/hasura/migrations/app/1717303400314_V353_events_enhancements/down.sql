
DROP INDEX IF EXISTS "public"."events_cloudaccount_findingid";

CREATE  INDEX "events_id_findingid_tenant" on
  "public"."events" using btree ("finding_id", "id", "tenant");

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."events" add column "principal" text
--  null;
