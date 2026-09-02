DROP INDEX IF EXISTS "public"."idx_recommendation_vulnerability_id";
ALTER TABLE "public"."recommendation" DROP COLUMN IF EXISTS "vulnerability_id";
DROP TABLE IF EXISTS "public"."vulnerabilities";
