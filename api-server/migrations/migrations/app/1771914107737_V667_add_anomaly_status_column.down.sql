DROP INDEX IF EXISTS idx_anomaly_status_type;
ALTER TABLE "public"."anomaly" DROP COLUMN IF EXISTS "anomaly_status";
