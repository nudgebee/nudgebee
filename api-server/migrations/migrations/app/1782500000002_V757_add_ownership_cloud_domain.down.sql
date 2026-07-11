DROP INDEX IF EXISTS "public"."ownership_rules_tenant_domain_idx";
ALTER TABLE "public"."ownership_rules" DROP COLUMN IF EXISTS "resource_domain";
