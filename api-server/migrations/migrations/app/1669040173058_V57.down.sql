
alter table "public"."projects" drop constraint "projects_category_fkey";

DELETE FROM "public"."project_category_type" WHERE "value" = 'R&D';

DELETE FROM "public"."project_category_type" WHERE "value" = 'Delivery';

DELETE FROM "public"."project_category_type" WHERE "value" = 'Training';

DELETE FROM "public"."project_category_type" WHERE "value" = 'Internal';

DROP TABLE "public"."project_category_type";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."projects" add column "category" Text
--  null default 'Internal';
