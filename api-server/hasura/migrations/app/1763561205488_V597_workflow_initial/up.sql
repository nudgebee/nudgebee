
CREATE TABLE IF NOT EXISTS workflows (
    id uuid DEFAULT gen_random_uuid() PRIMARY KEY NOT NULL,
    tenant_id uuid NOT NULL,
    account_id uuid NOT NULL,
    name VARCHAR(255) NOT NULL,
    definition JSONB NOT NULL,
    tags JSONB NOT NULL,
    status VARCHAR(50) NOT NULL,
    created_by uuid,
    updated_by uuid,
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT now(),
    updated_at TIMESTAMP WITHOUT TIME ZONE DEFAULT now()
);


ALTER TABLE public.workflows DROP CONSTRAINT IF EXISTS workflow_tenant_id_fkey;
ALTER TABLE public.workflows ADD CONSTRAINT workflow_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenant(id) ON DELETE RESTRICT ON UPDATE RESTRICT;

ALTER TABLE public.workflows DROP CONSTRAINT IF EXISTS workflows_update_by_fkey;
ALTER TABLE public.workflows ADD CONSTRAINT workflows_update_by_fkey FOREIGN KEY (updated_by) REFERENCES public.users(id) ON DELETE RESTRICT ON UPDATE RESTRICT;

ALTER TABLE public.workflows DROP CONSTRAINT IF EXISTS workflows_account_id_fkey;
ALTER TABLE public.workflows ADD CONSTRAINT workflows_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.cloud_accounts(id) ON DELETE RESTRICT ON UPDATE RESTRICT;

ALTER TABLE public.workflows DROP CONSTRAINT IF EXISTS workflows_created_by_fkey;
ALTER TABLE public.workflows ADD CONSTRAINT workflows_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE RESTRICT ON UPDATE RESTRICT;

CREATE INDEX IF NOT EXISTS idx_workflows_tenant_account_id ON workflows (tenant_id, account_id, id);
CREATE INDEX IF NOT EXISTS idx_workflows_tenant_account_name ON workflows (tenant_id, account_id, name);

create trigger set_public_workflows_updated_at before
update
    on
    public.workflows for each row execute function set_current_timestamp_updated_at();