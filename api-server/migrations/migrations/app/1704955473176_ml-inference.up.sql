CREATE TABLE public.ml_inference (
	id uuid NOT NULL,
	inference_time timestamp NOT NULL,
	tenant_id uuid NOT NULL,
	account_id uuid NOT NULL,
	"namespace" text NOT NULL,
	deployment text NOT NULL,
	model text NOT NULL,
	replicas text NULL,
	cpu text NULL,
	memory text NULL,
	CONSTRAINT ml_inference_pk PRIMARY KEY (id),
	CONSTRAINT ml_inference_un UNIQUE (inference_time, tenant_id, account_id, namespace, deployment, model)
);