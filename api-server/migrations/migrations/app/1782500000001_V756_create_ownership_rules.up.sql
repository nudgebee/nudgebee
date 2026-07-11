-- ownership_rules: declarative rules that assign a resource owner from a K8s
-- workload label or namespace. Evaluated lazily at resolve time (never materialized
-- into resource_owners). Single-condition exact match in this phase.
CREATE TABLE IF NOT EXISTS "public"."ownership_rules" (
    "id" uuid NOT NULL DEFAULT gen_random_uuid(),
    "tenant_id" uuid NOT NULL,
    "name" text NOT NULL,
    "match_scope" text NOT NULL,                  -- 'label' | 'namespace'
    "match_key" text,                             -- label key (match_scope='label'); unused for 'namespace'
    "match_value" text NOT NULL,                  -- label value, or namespace name
    "cloud_account_id" uuid,                       -- optional: restrict to one cluster/account (null = all)
    "owner_type" text NOT NULL,                   -- 'user' | 'group'
    "owner_id" uuid NOT NULL,                      -- users.id | user_groups.id
    "priority" integer NOT NULL DEFAULT 100,      -- lower wins on multi-match; tie-break created_at, id
    "enabled" boolean NOT NULL DEFAULT true,
    "created_by" uuid,
    "updated_by" uuid,
    "created_at" timestamptz NOT NULL DEFAULT now(),
    "updated_at" timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY ("id"),
    FOREIGN KEY ("cloud_account_id") REFERENCES "public"."cloud_accounts"("id") ON UPDATE restrict ON DELETE cascade
);

-- Load a tenant's enabled rules for evaluation.
CREATE INDEX IF NOT EXISTS "ownership_rules_tenant_enabled_idx"
    ON "public"."ownership_rules" ("tenant_id", "enabled");
