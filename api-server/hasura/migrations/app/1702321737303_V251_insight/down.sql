
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."insight" add column "rule" jsonb
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP table "public"."insights_summary";


DROP INDEX IF EXISTS "public"."insight_tenant_account_id_source_key";

DROP TABLE "public"."insight";
