
alter table "public"."recommendation" drop constraint "recommendation_category_fkey";

DELETE FROM "public"."recommendation_category_type" WHERE "value" = 'InfraUpgrade';

DELETE FROM "public"."recommendation_category_type" WHERE "value" = 'Security';

DELETE FROM "public"."recommendation_category_type" WHERE "value" = 'RightSizing';

DROP TABLE "public"."recommendation_category_type";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."recommendation" add column "category" text
--  null default 'RightSizing';
