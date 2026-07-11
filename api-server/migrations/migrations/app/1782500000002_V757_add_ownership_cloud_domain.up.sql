-- Phase 2: extend ownership rules to the cloud domain (per-resource owners for
-- EC2/RDS/S3/…). resource_domain partitions rules into 'k8s' (existing) and
-- 'cloud' so each resolver only sees its own rows. DEFAULT 'k8s' backfills all
-- existing rules correctly.
ALTER TABLE "public"."ownership_rules"
  ADD COLUMN IF NOT EXISTS "resource_domain" text NOT NULL DEFAULT 'k8s';

CREATE INDEX IF NOT EXISTS "ownership_rules_tenant_domain_idx"
  ON "public"."ownership_rules" ("tenant_id", "resource_domain", "enabled");
