DROP INDEX IF EXISTS idx_workflow_templates_global;
ALTER TABLE workflow_templates ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE workflow_templates ALTER COLUMN account_id SET NOT NULL;
