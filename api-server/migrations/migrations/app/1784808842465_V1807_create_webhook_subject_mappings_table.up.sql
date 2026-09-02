-- Dedicated table for webhook subject (title -> service) mappings, previously
-- stored as JSON blobs in tenant_attrs under keys like
-- DATADOG_INCIDENT_TITLE_SERVICE_MAPPING. tenant_attrs is a generic per-tenant
-- key/value store; this data has its own shape and its own writers/readers
-- (sync jobs + the webhook_subject_name_extractor agent), so it gets its own table.
--
-- One row per (tenant_id, attr_key, title) instead of one JSON blob per
-- (tenant_id, attr_key): a title can legitimately resolve to more than one
-- real service over time (the same generic alert title fires for many
-- different pods/workloads), so services holds a comma-separated list rather
-- than collapsing to a single last-write-wins value.
--
-- account_id is included for a future per-account scoping of mappings but is
-- left nullable and unused for now; all rows are tenant-scoped until that is
-- needed.
CREATE TABLE IF NOT EXISTS webhook_subject_mappings (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL,
    account_id uuid,
    attr_key   varchar(255) NOT NULL,
    title      text NOT NULL,
    services   text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT webhook_subject_mappings_tenant_id_fkey
        FOREIGN KEY (tenant_id) REFERENCES tenant(id) ON DELETE RESTRICT ON UPDATE RESTRICT
);

-- Case-insensitive uniqueness on title per (tenant, source), and the index
-- mergeAndSaveMappings/LearnSubjectMapping upsert against.
CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_subject_mappings_tenant_attr_title
    ON webhook_subject_mappings (tenant_id, attr_key, lower(title));
