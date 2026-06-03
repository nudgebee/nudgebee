
DROP INDEX IF EXISTS "public"."resource_id_attrs_name_type";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."resource_attrs" add column "type" text
--  not null;

DROP TABLE "public"."resource_attrs";
