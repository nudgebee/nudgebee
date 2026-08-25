ALTER TABLE "public"."slo_report" DROP COLUMN IF EXISTS "burn_rates";
ALTER TABLE "public"."slo_report" DROP COLUMN IF EXISTS "severity";

-- Reports written while NO_DATA existed must move back to a status the
-- pre-migration FK accepts before the row can be removed from slo_status.
UPDATE "public"."slo_report" SET "status" = 'OK' WHERE "status" = 'NO_DATA';
DELETE FROM "public"."slo_status" WHERE "value" = 'NO_DATA';
