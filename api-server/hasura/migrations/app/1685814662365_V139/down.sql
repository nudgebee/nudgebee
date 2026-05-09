
alter table "public"."tenant" drop constraint "tenant_type_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."tenant" add column "type" Text
--  null default 'Customer';

DELETE FROM "public"."tenant_type" WHERE "value" = 'Customer';

DELETE FROM "public"."tenant_type" WHERE "value" = 'Demo';

DELETE FROM "public"."tenant_type" WHERE "value" = 'QA';

DROP TABLE "public"."tenant_type";
