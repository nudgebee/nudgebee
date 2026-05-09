create table if not exists llm_rags(
	id uuid primary key default gen_random_uuid() not null,
	tenant_id uuid not null,
	account_id uuid,
	agent_id varchar,
	data text not null,
	created_by uuid,
	updated_by uuid,
	created_at timestamp default now(),
	updated_at timestamp default now()
);

ALTER TABLE public.llm_rags drop constraint if exists llm_rag_account_id_fkey;
ALTER TABLE public.llm_rags ADD constraint llm_rag_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.cloud_accounts(id) ON DELETE RESTRICT ON UPDATE RESTRICT;

ALTER TABLE public.llm_rags drop constraint if exists llm_rag_tenant_id_fkey;
ALTER TABLE public.llm_rags ADD constraint llm_rag_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenant(id) ON DELETE RESTRICT ON UPDATE RESTRICT;

ALTER TABLE public.llm_rags drop constraint if exists llm_rag_created_by_fkey;
ALTER TABLE public.llm_rags ADD CONSTRAINT llm_rag_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE RESTRICT ON UPDATE RESTRICT;

ALTER TABLE public.llm_rags drop constraint if exists llm_rag_updated_by_fkey;
ALTER TABLE public.llm_rags ADD CONSTRAINT llm_rag_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES public.users(id) ON DELETE RESTRICT ON UPDATE RESTRICT;

