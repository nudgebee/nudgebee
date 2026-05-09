-- public.insights_summary definition

-- Drop table

-- DROP TABLE public.insights_summary;

CREATE TABLE public.insights_summary (
	id uuid NOT NULL DEFAULT gen_random_uuid(),
	updated_at timestamp NOT NULL DEFAULT now(),
	tenant_id uuid NOT NULL,
	entity_type text NOT NULL,
	entity_id text NOT NULL,
	entity_name text NOT NULL,
	"name" text NOT NULL,
	description text NOT NULL,
	CONSTRAINT entity_type_check CHECK ((entity_type = ANY (ARRAY['tenant'::text, 'account'::text, 'service'::text, 'resource'::text]))),
	CONSTRAINT insights_summary_pkey PRIMARY KEY (id),
	CONSTRAINT insights_summary_tenant_id_entity_type_entity_id_name_key UNIQUE (tenant_id, entity_type, entity_id, name)
);


-- public.insights_summary foreign keys

ALTER TABLE public.insights_summary ADD CONSTRAINT insights_summary_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenant(id) ON DELETE RESTRICT ON UPDATE RESTRICT;