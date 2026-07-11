-- resource_owners: who owns a resource (K8s workload, namespace, cluster, cloud
-- account, or KG service). Stores MANUAL assignments only — rule-based ownership is
-- computed at read time (lazy), never written here. One owner (user or group) per
-- resource.
CREATE TABLE IF NOT EXISTS "public"."resource_owners" (
    "id" uuid NOT NULL DEFAULT gen_random_uuid(),
    "tenant_id" uuid NOT NULL,
    "resource_type" text NOT NULL,                -- 'workload'|'namespace'|'cluster'|'cloud_account'|'service'
    "resource_key" text NOT NULL,                 -- workload=cloud_resource_id; namespace=<cloud_account_id>/<namespace>;
                                                  --   cluster|cloud_account=<cloud_accounts.id>; service=KG unique_key
    "cloud_account_id" uuid,                       -- denormalized for fast filter + inheritance
    "owner_type" text NOT NULL,                   -- 'user' | 'group'
    "owner_id" uuid NOT NULL,                      -- users.id | user_groups.id
    "created_by" uuid,
    "updated_by" uuid,
    "created_at" timestamptz NOT NULL DEFAULT now(),
    "updated_at" timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY ("id"),
    -- Deleting a cloud account purges its resources' owner rows (orphan cleanup).
    FOREIGN KEY ("cloud_account_id") REFERENCES "public"."cloud_accounts"("id") ON UPDATE restrict ON DELETE cascade,
    CONSTRAINT "resource_owners_tenant_type_key_uniq"
        UNIQUE ("tenant_id", "resource_type", "resource_key")
);

-- "resources manually owned by X".
CREATE INDEX IF NOT EXISTS "resource_owners_tenant_owner_idx"
    ON "public"."resource_owners" ("tenant_id", "owner_type", "owner_id");

-- Inheritance / per-account filtering.
CREATE INDEX IF NOT EXISTS "resource_owners_tenant_type_account_idx"
    ON "public"."resource_owners" ("tenant_id", "resource_type", "cloud_account_id");
