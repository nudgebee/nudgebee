
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."recommendation" add column "dismissed_reason" text
--  null;

DELETE FROM "public"."recommendation_status_type" WHERE "value" = 'Assigned';

DELETE FROM "public"."recommendation_status_type" WHERE "value" = 'Dismissed';

DELETE FROM "public"."recommendation_status_type" WHERE "value" = 'Closed';
