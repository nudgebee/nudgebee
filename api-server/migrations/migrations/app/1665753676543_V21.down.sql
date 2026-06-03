

alter table "public"."spends" drop constraint "spends_resource_group_fkey";

DELETE FROM "public"."spends_resource_group_types" WHERE "value" = 'AmazonCloudWatch';

DELETE FROM "public"."spends_resource_group_types" WHERE "value" = 'AmazonS3';

DELETE FROM "public"."spends_resource_group_types" WHERE "value" = 'AWSLambda';

DELETE FROM "public"."spends_resource_group_types" WHERE "value" = 'AmazonCloudFront';

DELETE FROM "public"."spends_resource_group_types" WHERE "value" = 'AmazonRoute53';

DROP TABLE "public"."spends_resource_group_types";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- drop view spends_count_aggregate;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE
-- OR REPLACE VIEW "public"."spends_amount_sum_daily_aggregate" AS
-- SELECT
--   s.tenant,
--   (s.date) :: date AS created_at_date,
--   sum(amount) AS amount
-- FROM
--   spends s
-- GROUP BY
--   s.tenant,
--   ((s.date) :: date)
-- ORDER BY
--   ((s.date) :: date);

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE
-- OR REPLACE VIEW "public"."spends_count_aggregate" AS
-- SELECT
--   s.tenant,
--   (s.date) :: date AS created_at_date,
--   count(*) AS count
-- FROM
--   spends s
-- GROUP BY
--   s.tenant,
--   ((s.date) :: date)
-- ORDER BY
--   ((s.date) :: date);
