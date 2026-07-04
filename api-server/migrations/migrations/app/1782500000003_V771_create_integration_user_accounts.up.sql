-- integration_user_accounts: pool of external integration accounts (Slack, GitHub,
-- PagerDuty, ZenDuty) discovered by the Identity Sync job, plus the optional mapping
-- to an internal Nudgebee user (auto-matched by email or set manually).
--
-- Scope follows the integration's category: tenant-scoped integrations
-- (messaging/ticketing — all four identity types today) store account_id = NULL,
-- one row per (tenant, integration instance, external user). Account-scoped
-- integration types instead store the cloud account and fan out one row per
-- (account, external user) from integrations_cloud_accounts.
CREATE TABLE IF NOT EXISTS "public"."integration_user_accounts" (
    "id" uuid NOT NULL DEFAULT gen_random_uuid(),
    "tenant_id" uuid NOT NULL,
    "account_id" uuid,                            -- cloud_accounts.id, NULL for tenant-scoped integration types
    "integration_type" text NOT NULL,            -- 'slack' | 'github' | 'pagerduty' | 'zenduty'
    "integration_id" uuid,                        -- which configured integration instance produced this account
    "external_user_id" text NOT NULL,             -- Slack Uxxx, PD/ZD user id, GitHub login
    "external_username" text,
    "external_email" text,                        -- null for github (collaborators API exposes login only)
    "external_display_name" text,
    "profile" jsonb NOT NULL DEFAULT '{}',
    "mapped_user_id" uuid,                         -- users.id, null until matched
    "mapped_via" text,                            -- 'auto_email' | 'manual' | null
    "mapped_by" uuid,                             -- users.id who performed a manual map
    "is_active" boolean NOT NULL DEFAULT true,    -- false = tombstoned (removed at source); manual mappings are kept inactive and reactivate if the account reappears
    "last_synced_at" timestamptz,
    "created_at" timestamptz NOT NULL DEFAULT now(),
    "updated_at" timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY ("id"),
    FOREIGN KEY ("account_id") REFERENCES "public"."cloud_accounts"("id") ON UPDATE restrict ON DELETE cascade,
    FOREIGN KEY ("integration_id") REFERENCES "public"."integrations"("id") ON UPDATE restrict ON DELETE cascade,
    FOREIGN KEY ("mapped_user_id") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE set null,
    FOREIGN KEY ("mapped_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE set null
);

-- Unique identity per (tenant, account, integration instance, external user).
-- account_id is NULL for tenant-scoped types, so it is coalesced to a fixed
-- sentinel here (and in the upsert's ON CONFLICT) to dedupe those rows.
CREATE UNIQUE INDEX IF NOT EXISTS "integration_user_accounts_identity_uidx"
    ON "public"."integration_user_accounts"
    ("tenant_id", COALESCE("account_id", '00000000-0000-0000-0000-000000000000'::uuid),
     "integration_type", "integration_id", "external_user_id");

-- Show all synced profiles for a given internal user, grouped by account (Edit User page).
CREATE INDEX IF NOT EXISTS "integration_user_accounts_tenant_acct_mapped_user_idx"
    ON "public"."integration_user_accounts" ("tenant_id", "account_id", "mapped_user_id");

-- Manual-map picker: list unmapped accounts of a given integration type.
CREATE INDEX IF NOT EXISTS "integration_user_accounts_unmapped_idx"
    ON "public"."integration_user_accounts" ("tenant_id", "integration_type")
    WHERE "mapped_user_id" IS NULL;

-- Case-insensitive email auto-match against users.username.
CREATE INDEX IF NOT EXISTS "integration_user_accounts_lower_email_idx"
    ON "public"."integration_user_accounts" ("tenant_id", lower("external_email"));
