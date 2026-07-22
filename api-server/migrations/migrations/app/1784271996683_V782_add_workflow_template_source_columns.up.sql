-- Add provenance/sync columns to workflow_templates so system templates can be
-- loaded from multiple sources (migration | bundled | github | url) and reconciled
-- idempotently by runbook-server. See runbook-server/internal/templatesource.
--
-- Identity for system rows is `external_key` ALONE (not (source, external_key)):
-- migration -> bundled -> github supersede each other in place on the same row.

ALTER TABLE workflow_templates
  ADD COLUMN IF NOT EXISTS source       text NOT NULL DEFAULT 'migration',
  ADD COLUMN IF NOT EXISTS external_key text NULL,
  ADD COLUMN IF NOT EXISTS checksum     text NULL,
  ADD COLUMN IF NOT EXISTS source_ref   text NULL,
  ADD COLUMN IF NOT EXISTS synced_at    timestamptz NULL;

-- One row per slug for system (tenant/account-less) templates. Plain
-- (non-CONCURRENT) build: the table is tiny and golang-migrate wraps each
-- migration in a transaction, where CONCURRENTLY is illegal.
CREATE UNIQUE INDEX IF NOT EXISTS uq_workflow_templates_external_key
  ON workflow_templates (external_key)
  WHERE external_key IS NOT NULL AND tenant_id IS NULL AND account_id IS NULL;
