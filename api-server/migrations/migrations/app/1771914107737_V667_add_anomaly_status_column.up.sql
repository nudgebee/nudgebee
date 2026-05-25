ALTER TABLE "public"."anomaly" ADD COLUMN IF NOT EXISTS "anomaly_status" text;

CREATE INDEX idx_anomaly_status_type ON "public"."anomaly" (anomaly_status, anomaly_type)
    WHERE anomaly_status = 'OPEN';

-- Backfill existing spend anomalies as RESOLVED
UPDATE "public"."anomaly" SET anomaly_status = 'RESOLVED'
    WHERE anomaly_type IN ('CloudSpendAccount', 'CloudSpendService') AND anomaly_status IS NULL;
