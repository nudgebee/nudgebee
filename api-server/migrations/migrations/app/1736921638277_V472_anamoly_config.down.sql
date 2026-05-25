
alter table "public"."anomaly_config" alter column "threshold" drop not null;
alter table "public"."anomaly_config" add column "threshold" numeric;

alter table "public"."anomaly_config" alter column "reference_query" drop not null;
alter table "public"."anomaly_config" add column "reference_query" text;

alter table "public"."anomaly_config" drop constraint "anomaly_config_change_operator_fkey";

alter table "public"."anomaly_config" drop constraint "anomaly_config_anomaly_type_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."anomaly_config" add column "anomaly_type" text
--  not null;

DELETE FROM "public"."anomaly_type" WHERE "value" = 'APIRequest';

DELETE FROM "public"."anomaly_type" WHERE "value" = 'Network';

DELETE FROM "public"."anomaly_change_operator" WHERE "value" = 'GTE';

DELETE FROM "public"."anomaly_change_operator" WHERE "value" = 'LTE';

DELETE FROM "public"."anomaly_change_operator" WHERE "value" = 'LT';

DELETE FROM "public"."anomaly_change_operator" WHERE "value" = 'GT';

alter table "public"."anomaly_change_operator" alter column "comment" set not null;

DROP TABLE "public"."anomaly_change_operator";

DELETE FROM "public"."anomaly_type" WHERE "value" = 'ErrorRate';

DELETE FROM "public"."anomaly_type" WHERE "value" = 'CPU';

DELETE FROM "public"."anomaly_type" WHERE "value" = 'Memory';

DELETE FROM "public"."anomaly_type" WHERE "value" = 'Latency';

DROP TABLE "public"."anomaly_type";

alter table "public"."anomaly_config" alter column "threshold" set not null;

ALTER TABLE "public"."anomaly_config" ALTER COLUMN "buffer_percentage" drop default;

alter table "public"."anomaly_config" alter column "reference_query" set not null;


DROP TABLE "public"."anomaly_config";

DELETE FROM "public"."anomaly_config_type" WHERE "value" = 'Metric';

alter table "public"."anomaly_config_type" rename to "anamoly_config_type";

DROP TABLE "public"."anamoly_config_type";
