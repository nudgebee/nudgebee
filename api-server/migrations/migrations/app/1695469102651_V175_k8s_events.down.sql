

alter table "public"."events" alter column "failure" set not null;

alter table "public"."events" alter column "starts_at" set not null;

alter table "public"."events" alter column "ends_at" set not null;

alter table "public"."events" alter column "description" set not null;

alter table "public"."events" alter column "category" set not null;


DROP TABLE "public"."agent";

DROP INDEX IF EXISTS "public"."event_tenant_cloud_account_id";

alter table "public"."events" drop constraint "events_cloud_account_id_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."events" add column "cloud_account_id" uuid
--  null;


ALTER TABLE "public"."events" ALTER COLUMN "updated_at" TYPE timestamp with time zone;

ALTER TABLE "public"."events" ALTER COLUMN "created_at" TYPE timestamp with time zone;

ALTER TABLE "public"."events" ALTER COLUMN "starts_at" TYPE timestamp with time zone;

ALTER TABLE "public"."events" ALTER COLUMN "ends_at" TYPE timestamp with time zone;

DROP INDEX IF EXISTS "public"."events_id_findingid_tenant";

alter table "public"."events" drop constraint "events_source_fkey";

DELETE FROM "public"."event_source" WHERE "value" = 'scheduler';

DELETE FROM "public"."event_source" WHERE "value" = 'callback';

DELETE FROM "public"."event_source" WHERE "value" = 'helm_release';

DELETE FROM "public"."event_source" WHERE "value" = 'manual';

DELETE FROM "public"."event_source" WHERE "value" = 'prometheus';

DELETE FROM "public"."event_source" WHERE "value" = 'kubernetes_api_server';

DROP TABLE "public"."event_source";

alter table "public"."events" drop constraint "events_priority_fkey";

DROP TABLE "public"."events";

DELETE FROM "public"."event_status" WHERE "value" = 'RESOLVED';

DELETE FROM "public"."event_status" WHERE "value" = 'FIRING';

DROP TABLE "public"."event_status";

DELETE FROM "public"."event_severity" WHERE "value" = 'HIGH';

DELETE FROM "public"."event_severity" WHERE "value" = 'MEDIUM';

DELETE FROM "public"."event_severity" WHERE "value" = 'LOW';

DELETE FROM "public"."event_severity" WHERE "value" = 'INFO';

DELETE FROM "public"."event_severity" WHERE "value" = 'DEBUG';

DROP TABLE "public"."event_severity";
