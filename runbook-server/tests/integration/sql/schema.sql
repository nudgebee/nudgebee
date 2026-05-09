CREATE TABLE IF NOT EXISTS cloud_accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant VARCHAR(255) NOT NULL
);

insert into cloud_accounts(id, tenant) values ('055a8f9e-08dc-4a4b-b378-e5fd206d8f4a', 'bbff8859-3976-43e1-bf69-28075a806965') on conflict (id) do nothing;

CREATE TABLE IF NOT EXISTS workflows (
	id uuid DEFAULT gen_random_uuid() PRIMARY KEY NOT NULL,
    tenant_id uuid NOT NULL,
    account_id uuid NOT NULL,
    name VARCHAR(255) NOT NULL,
    definition JSONB NOT NULL,
    tags JSONB NOT NULL,
    status VARCHAR(50) NOT NULL, -- Administrative status (e.g., Active, Inactive)
    last_execution_status VARCHAR(50), -- Status of the last execution (e.g., RUNNING, COMPLETED)
    last_execution_status_message text,
    last_execution_time TIMESTAMP WITHOUT TIME ZONE,
    created_by uuid,
    updated_by uuid,
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT now(),
    updated_at TIMESTAMP WITHOUT TIME ZONE DEFAULT now()
);

CREATE TABLE IF NOT EXISTS workflow_state (
    workflow_id uuid NOT NULL,
    key VARCHAR(255) NOT NULL,
    value JSONB NOT NULL,
    updated_at TIMESTAMP WITHOUT TIME ZONE DEFAULT now(),
    expires_at TIMESTAMP WITHOUT TIME ZONE,
    last_updated_by_execution_id VARCHAR(255),
    last_updated_by_task_id VARCHAR(255),
    PRIMARY KEY (workflow_id, key),
    CONSTRAINT fk_workflow
      FOREIGN KEY(workflow_id) 
      REFERENCES workflows(id)
      ON DELETE CASCADE
);

-- ALTER TABLE public.workflows ADD CONSTRAINT workflow_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenant(id) ON DELETE RESTRICT ON UPDATE RESTRICT;

-- ALTER TABLE public.workflows ADD CONSTRAINT workflows_update_by_fkey FOREIGN KEY (updated_by) REFERENCES public.users(id) ON DELETE RESTRICT ON UPDATE RESTRICT;

-- ALTER TABLE public.workflows ADD CONSTRAINT workflows_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.cloud_accounts(id) ON DELETE RESTRICT ON UPDATE RESTRICT;

-- ALTER TABLE public.workflows ADD CONSTRAINT workflows_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE RESTRICT ON UPDATE RESTRICT;


CREATE INDEX IF NOT EXISTS idx_workflows_tenant_account_id ON workflows (tenant_id, account_id, id);

CREATE INDEX IF NOT EXISTS idx_workflows_tenant_account_name ON workflows (tenant_id, account_id, name);

CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE IF NOT EXISTS configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key CITEXT NOT NULL,
    value TEXT NOT NULL,
    type VARCHAR(50) NOT NULL CHECK (type IN ('config', 'secret')),
    labels JSONB,
    metadata JSONB,
    tenant_id UUID NOT NULL,
    account_id UUID NOT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT now(),
    updated_at TIMESTAMP WITHOUT TIME ZONE DEFAULT now(),
    created_by UUID,
    updated_by UUID,
    CONSTRAINT unique_account_key UNIQUE (account_id, key)
);

CREATE INDEX IF NOT EXISTS idx_configs_account_id ON configs (account_id);
