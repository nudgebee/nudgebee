DROP TABLE IF EXISTS recommendation_resolution CASCADE;
DROP TABLE IF EXISTS agent_task CASCADE;
DROP TABLE IF EXISTS agent CASCADE;
DROP TABLE IF EXISTS auto_optimize_resource_map CASCADE;
DROP TABLE IF EXISTS cloud_resourses CASCADE;
DROP TABLE IF EXISTS recommendation CASCADE;
DROP TABLE IF EXISTS auto_pilot_task CASCADE;
DROP TABLE IF EXISTS auto_pilot CASCADE;

CREATE TABLE IF NOT EXISTS auto_pilot (
	id uuid DEFAULT gen_random_uuid() NOT NULL,
	"name" varchar NOT NULL,
	account_id uuid NOT NULL,
	"rule" jsonb NOT NULL,
	creation_date timestamp DEFAULT now() NOT NULL,
	update_date timestamp DEFAULT now() NOT NULL,
	created_by uuid NOT NULL,
	schedule_time varchar NULL,
	status text DEFAULT 'Active'::text NOT NULL,
	"source" text NULL,
	last_schedule_time timestamp NULL,
	last_executed_time timestamp NULL,
	tenant_id uuid NOT NULL,
	category varchar NOT NULL,
	start_at timestamp DEFAULT now() NOT NULL,
	end_at timestamp NULL,
	notification jsonb NULL,
	next_schedule_time timestamp NULL,
	execution_status text DEFAULT 'Idle'::text NULL,
	update_by uuid NULL,
	"attributes" jsonb DEFAULT jsonb_build_object() NOT NULL,
	CONSTRAINT schedules_pkey PRIMARY KEY (id)
);

CREATE TABLE IF NOT EXISTS auto_pilot_task (
	id uuid DEFAULT gen_random_uuid() NOT NULL,
	task_id uuid NULL,
	auto_pilot_id uuid NOT NULL,
	scheduled_time timestamp NOT NULL,
	recommendation_id uuid NULL,
	"name" varchar NOT NULL,
	status text DEFAULT 'Scheduled'::text NOT NULL,
	reason text NULL,
	command text NULL,
	tenant_id uuid NOT NULL,
	created_at timestamp DEFAULT now() NOT NULL,
	updated_at timestamp DEFAULT now() NOT NULL,
	meta jsonb DEFAULT jsonb_build_object() NOT NULL,
	"error" text NULL,
	"attributes" jsonb DEFAULT jsonb_build_object() NOT NULL,
	resource_filter jsonb DEFAULT jsonb_build_object() NULL,
	skipped_by uuid NULL,
	account_id uuid NOT NULL,
	CONSTRAINT scheduled_task_pkey PRIMARY KEY (id),
	CONSTRAINT scheduled_task_task_id_schedule_id_key UNIQUE (task_id, auto_pilot_id)
);

CREATE TABLE IF NOT EXISTS recommendation (
	id uuid DEFAULT gen_random_uuid() NOT NULL,
	created_at timestamp DEFAULT now() NOT NULL,
	updated_at timestamp DEFAULT now() NOT NULL,
	tenant_id uuid NOT NULL,
	cloud_account_id uuid NOT NULL,
	resource_id uuid NULL,
	recommendation jsonb NOT NULL,
	recommendation_action text NOT NULL,
	note text NULL,
	severity text DEFAULT 'Medium'::text NULL,
	estimated_savings float8 DEFAULT 0 NULL,
	status text DEFAULT 'Open'::text NULL,
	category text DEFAULT 'RightSizing'::text NULL,
	rule_name text NULL,
	dismissed_reason text NULL,
	is_dismissed bool DEFAULT false NOT NULL,
	account_object_id text NULL,
	updated_by uuid NULL,
	CONSTRAINT recommendation_account_object_id_resource_id_cloud_account_id_r UNIQUE (account_object_id, resource_id, cloud_account_id, rule_name, category),
	CONSTRAINT recommendation_cloud_account_id_rule_name_resource_id_category_ UNIQUE (cloud_account_id, rule_name, resource_id, category, account_object_id),
	CONSTRAINT recommendation_pkey PRIMARY KEY (id),
	CONSTRAINT recommendation_rule_name_cloud_account_id_account_object_id_cat UNIQUE (rule_name, cloud_account_id, account_object_id, category, resource_id)
);

CREATE TABLE IF NOT EXISTS cloud_resourses (
	id uuid DEFAULT gen_random_uuid() NOT NULL,
	created_at timestamp DEFAULT now() NOT NULL,
	created_by uuid NULL,
	updated_at timestamp DEFAULT now() NOT NULL,
	updated_by uuid NULL,
	resourse_id text NULL,
	"name" text NULL,
	"type" text NULL,
	status text DEFAULT 'Active'::text NULL,
	resourse_created_on timestamp NULL,
	account uuid NOT NULL,
	cloud_provider text NOT NULL,
	region text NOT NULL,
	arn text NULL,
	tenant uuid NOT NULL,
	tags jsonb NULL,
	meta jsonb NULL,
	service_name text NULL,
	first_seen timestamp NULL,
	last_seen timestamp NULL,
	is_active bool NULL,
	external_resource_id text DEFAULT gen_random_uuid()::text NULL,
	CONSTRAINT cloud_resourses_account_external_resource_id_key UNIQUE (account, external_resource_id),
	CONSTRAINT cloud_resourses_account_resourse_service_type_region_key UNIQUE (account, resourse_id, type, region, service_name),
	CONSTRAINT cloud_resourses_pkey PRIMARY KEY (id)
);

CREATE TABLE IF NOT EXISTS auto_optimize_resource_map (
	id uuid DEFAULT gen_random_uuid() NOT NULL,
	resource_identifier jsonb NOT NULL,
	auto_optimize_type text NOT NULL,
	auto_optimize_id uuid NOT NULL,
	tenant_id uuid NOT NULL,
	account_id uuid NOT NULL,
	CONSTRAINT auto_optimize_resource_map_pkey PRIMARY KEY (id)
);

CREATE TABLE IF NOT EXISTS agent (
	id uuid DEFAULT gen_random_uuid() NOT NULL,
	created_at timestamp DEFAULT now() NOT NULL,
	updated_at timestamp DEFAULT now() NOT NULL,
	tenant uuid NOT NULL,
	cloud_account_id uuid NOT NULL,
	"type" text NULL,
	status text NOT NULL,
	last_connected_at timestamp NULL,
	access_key text NULL,
	access_secret text NULL,
	status_message text NULL,
	last_synced_at timestamp NULL,
	"version" text NULL,
	k8s_version text NULL,
	connection_status jsonb NULL,
	k8s_provider text NULL,
	access_secret_v2 text NULL,
	CONSTRAINT agent_cloud_provider_canonical_check CHECK ((((lower(type) = ANY (ARRAY['aws'::text, 'azure'::text, 'gcp'::text])) AND (type = ANY (ARRAY['AWS'::text, 'Azure'::text, 'GCP'::text]))) OR (lower(type) <> ALL (ARRAY['aws'::text, 'azure'::text, 'gcp'::text])))),
	CONSTRAINT agent_pkey PRIMARY KEY (id),
	CONSTRAINT agent_tenant_account_type UNIQUE (tenant, cloud_account_id, type)
);

CREATE TABLE IF NOT EXISTS agent_task (
	id uuid DEFAULT gen_random_uuid() NOT NULL,
	created_at timestamptz DEFAULT now() NOT NULL,
	updated_at timestamptz DEFAULT now() NOT NULL,
	cloud_account_id uuid NOT NULL,
	tenant uuid NOT NULL,
	"action" text NOT NULL,
	payload jsonb NOT NULL,
	status text NULL,
	created_by uuid NULL,
	response jsonb NULL,
	agent_id uuid NULL,
	"source" text NULL,
	resoruce_id uuid NULL,
	source_id uuid NULL,
	CONSTRAINT agent_task_pkey PRIMARY KEY (id)
);

CREATE TABLE IF NOT EXISTS recommendation_resolution (
	id uuid DEFAULT gen_random_uuid() NOT NULL,
	recommendation_id uuid NOT NULL,
	"type" text NOT NULL,
	"data" jsonb NULL,
	status text NOT NULL,
	type_reference_id text NOT NULL,
	resolver_type text NOT NULL,
	resolver_id text NOT NULL,
	created_at timestamp DEFAULT now() NOT NULL,
	updated_at timestamp DEFAULT now() NULL,
	status_message text NULL,
	CONSTRAINT recommendation_resolution_pkey PRIMARY KEY (id),
	CONSTRAINT resolver_type_check CHECK ((resolver_type = ANY (ARRAY['User'::text, 'AutoOptimize'::text, 'AutoRunbook'::text]))),
	CONSTRAINT status_check CHECK ((status = ANY (ARRAY['InProgress'::text, 'Failed'::text, 'Success'::text, 'Configuring'::text]))),
	CONSTRAINT type_check CHECK ((type = ANY (ARRAY['PullRequest'::text, 'Ticket'::text, 'DeploymentChange'::text, 'EventResolution'::text, 'CloudResource'::text])))
);

CREATE UNIQUE INDEX IF NOT EXISTS agent_access_key ON agent USING btree (access_key);
CREATE INDEX IF NOT EXISTS agent_task_status_id_cloud_account_id_index_key ON agent_task USING btree (status, agent_id, cloud_account_id);