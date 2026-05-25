

CREATE
OR REPLACE VIEW "public"."spends_count_aggregate" AS
SELECT
  s.tenant,
  (s.date) :: date AS created_at_date,
  count(*) AS count
FROM
  spends s
GROUP BY
  s.tenant,
  ((s.date) :: date)
ORDER BY
  ((s.date) :: date);

CREATE
OR REPLACE VIEW "public"."spends_amount_sum_daily_aggregate" AS
SELECT
  s.tenant,
  (s.date) :: date AS created_at_date,
  sum(amount) AS amount
FROM
  spends s
GROUP BY
  s.tenant,
  ((s.date) :: date)
ORDER BY
  ((s.date) :: date);

drop view spends_count_aggregate;

CREATE TABLE "public"."spends_resource_group_types" ("value" text NOT NULL, "description" text NOT NULL, "cloud_provider" text NOT NULL, PRIMARY KEY ("value") , FOREIGN KEY ("cloud_provider") REFERENCES "public"."cloud_provider"("value") ON UPDATE restrict ON DELETE restrict);COMMENT ON TABLE "public"."spends_resource_group_types" IS E'Resource Groups';

INSERT INTO "public"."spends_resource_group_types"("value", "description", "cloud_provider") VALUES (E'AmazonRoute53', E'AmazonRoute53', E'AWS');

INSERT INTO "public"."spends_resource_group_types"("value", "description", "cloud_provider") VALUES (E'AmazonCloudFront', E'AmazonCloudFront', E'AWS');

INSERT INTO "public"."spends_resource_group_types"("value", "description", "cloud_provider") VALUES (E'AWSLambda', E'AWSLambda', E'AWS');

INSERT INTO "public"."spends_resource_group_types"("value", "description", "cloud_provider") VALUES (E'AmazonS3', E'AmazonS3', E'AWS');

INSERT INTO "public"."spends_resource_group_types"("value", "description", "cloud_provider") VALUES (E'AmazonCloudWatch', E'AmazonCloudWatch', E'AWS');

alter table "public"."spends"
  add constraint "spends_resource_group_fkey"
  foreign key ("resource_group")
  references "public"."spends_resource_group_types"
  ("value") on update restrict on delete restrict;
