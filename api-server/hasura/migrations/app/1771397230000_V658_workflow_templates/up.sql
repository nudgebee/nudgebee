
CREATE TABLE IF NOT EXISTS workflow_templates (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
    account_id      uuid NOT NULL REFERENCES cloud_accounts(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
    name            VARCHAR(255) NOT NULL,
    description     TEXT,
    category        VARCHAR(100),
    icon            VARCHAR(255),
    definition      JSONB NOT NULL,
    template_variables JSONB DEFAULT '[]'::jsonb,
    tags            JSONB DEFAULT '{}'::jsonb,
    is_system       BOOLEAN DEFAULT false,
    status          VARCHAR(50) DEFAULT 'ACTIVE' NOT NULL,
    created_by      uuid REFERENCES users(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
    updated_by      uuid REFERENCES users(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
    created_at      TIMESTAMP WITHOUT TIME ZONE DEFAULT now(),
    updated_at      TIMESTAMP WITHOUT TIME ZONE DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_workflow_templates_tenant_account ON workflow_templates (tenant_id, account_id);
CREATE INDEX IF NOT EXISTS idx_workflow_templates_category ON workflow_templates (tenant_id, account_id, category);
CREATE INDEX IF NOT EXISTS idx_workflow_templates_name ON workflow_templates (tenant_id, account_id, name);

CREATE TRIGGER set_public_workflow_templates_updated_at
    BEFORE UPDATE ON public.workflow_templates
    FOR EACH ROW
    EXECUTE FUNCTION set_current_timestamp_updated_at();
