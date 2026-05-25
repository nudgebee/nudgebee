UPDATE "public"."anomaly" SET namespace = '' WHERE namespace IS NULL;
ALTER TABLE "public"."anomaly" ALTER COLUMN "namespace" SET NOT NULL;
