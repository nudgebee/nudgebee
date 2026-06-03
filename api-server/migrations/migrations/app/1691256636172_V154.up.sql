-- public.metrics_summary definition

-- Drop table

-- DROP TABLE public.metrics_summary;

CREATE TABLE public.metrics_summary (
	tenant_id uuid NOT NULL,
	entity_type text NOT NULL,
	entity_id text NOT NULL,
	entity_name text NOT NULL,
	value float8 NULL,
	value_unit text NULL,
	"name" text NOT NULL,
	description text NULL,
	id uuid NOT NULL DEFAULT gen_random_uuid(),
	updated_at timestamp NOT NULL DEFAULT now(),
	CONSTRAINT entity_type_check CHECK ((entity_type = ANY (ARRAY['tenant'::text, 'account'::text, 'service'::text, 'resource'::text]))),
	CONSTRAINT metrics_summary_pkey PRIMARY KEY (id),
	CONSTRAINT metrics_summary_tenant_id_entity_type_entity_id_name_key UNIQUE (tenant_id, entity_type, entity_id, name)
);


-- public.metrics_summary foreign keys

ALTER TABLE public.metrics_summary ADD CONSTRAINT metrics_summary_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenant(id) ON DELETE RESTRICT ON UPDATE RESTRICT;