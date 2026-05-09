-- public.cloud_account_attrs definition

-- Drop table

-- DROP TABLE public.cloud_account_attrs;

CREATE TABLE public.cloud_account_attrs (
	id uuid NOT NULL DEFAULT gen_random_uuid(),
	name varchar(255) NOT NULL,
	value text NULL,
	created_at timestamp NOT NULL DEFAULT now(),
	updated_at timestamp NOT NULL DEFAULT now(),
	cloud_account_id uuid NOT NULL,
	CONSTRAINT cloud_account_attrs_pkey PRIMARY KEY (id)
);


-- public.cloud_account_attrs foreign keys

ALTER TABLE public.cloud_account_attrs ADD CONSTRAINT cloud_account_attrs_cloud_account_id_fkey FOREIGN KEY (cloud_account_id) REFERENCES public.cloud_accounts(id) ON DELETE RESTRICT ON UPDATE RESTRICT;




-- public.tenant_attrs definition

-- Drop table

-- DROP TABLE public.tenant_attrs;

CREATE TABLE public.tenant_attrs (
	id uuid NOT NULL DEFAULT gen_random_uuid(),
	"name" varchar(255) NOT NULL,
	value text NULL,
	created_at timestamp NOT NULL DEFAULT now(),
	updated_at timestamp NOT NULL DEFAULT now(),
	tenant_id uuid NOT NULL,
	CONSTRAINT tenant_attrs_pkey PRIMARY KEY (id)
);


-- public.tenant_attrs foreign keys

ALTER TABLE public.tenant_attrs ADD CONSTRAINT tenant_attrs_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenant(id) ON DELETE RESTRICT ON UPDATE RESTRICT;
