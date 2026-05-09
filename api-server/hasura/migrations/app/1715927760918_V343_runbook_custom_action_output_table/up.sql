-- public.runbook_task_output definition

-- Drop table

-- DROP TABLE public.runbook_task_output;

CREATE TABLE public.runbook_task_output (
	id uuid NOT NULL DEFAULT gen_random_uuid(),
	runbook_id uuid NOT NULL,
	task_id uuid NOT NULL,
	"output" bytea NOT NULL,
	tenant_id uuid NOT NULL,
	account_id uuid NOT NULL,
	CONSTRAINT runbook_task_output_pkey PRIMARY KEY (id),
	CONSTRAINT runbook_task_output_runbook_id_task_id_key UNIQUE (runbook_id, task_id)
);


-- public.runbook_task_output foreign keys

ALTER TABLE public.runbook_task_output ADD CONSTRAINT runbook_task_output_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.cloud_accounts(id) ON DELETE RESTRICT ON UPDATE RESTRICT;
ALTER TABLE public.runbook_task_output ADD CONSTRAINT runbook_task_output_runbook_id_fkey FOREIGN KEY (runbook_id) REFERENCES public.auto_playbook(id) ON DELETE RESTRICT ON UPDATE RESTRICT;
ALTER TABLE public.runbook_task_output ADD CONSTRAINT runbook_task_output_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.auto_playbook_task(id) ON DELETE RESTRICT ON UPDATE RESTRICT;
ALTER TABLE public.runbook_task_output ADD CONSTRAINT runbook_task_output_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenant(id) ON DELETE RESTRICT ON UPDATE RESTRICT;