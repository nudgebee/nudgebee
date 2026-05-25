
alter table "public"."event_correlations" drop constraint "event_correlations_related_event_id_event_id_cloud_account_id_key";
alter table "public"."event_correlations" add constraint "event_correlations_related_event_id_event_id_key" unique ("related_event_id", "event_id");

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."event_correlations" add column "tenant_id" uuid
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."event_correlations" add column "cloud_account_id" uuid
--  not null;

alter table "public"."event_duplicates" drop constraint "event_duplicates_event_id_cloud_account_id_key";

DROP INDEX IF EXISTS "public"."idx_event_corr_event";

DROP TABLE "public"."event_correlations";

ALTER TABLE "public"."event_duplicates" ALTER COLUMN "created_at" TYPE timestamp with time zone;

DROP INDEX IF EXISTS "public"."idx_event_dup_occurrence";

DROP INDEX IF EXISTS "public"."idx_event_dup_created";

DROP INDEX IF EXISTS "public"."idx_event_dup_first_event";

DROP INDEX IF EXISTS "public"."idx_event_dup_fingerprint";

DROP TABLE "public"."event_duplicates";
