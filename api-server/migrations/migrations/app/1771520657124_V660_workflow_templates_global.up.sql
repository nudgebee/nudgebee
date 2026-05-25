-- Make tenant_id and account_id nullable for global/static templates
ALTER TABLE workflow_templates ALTER COLUMN tenant_id DROP NOT NULL;
ALTER TABLE workflow_templates ALTER COLUMN account_id DROP NOT NULL;

-- Add index for global templates (where tenant_id IS NULL)
CREATE INDEX IF NOT EXISTS idx_workflow_templates_global
  ON workflow_templates (status) WHERE tenant_id IS NULL AND account_id IS NULL;
