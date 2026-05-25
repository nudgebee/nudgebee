ALTER TABLE agent drop constraint if exists agent_tenant_account_type;
ALTER TABLE agent ADD CONSTRAINT agent_tenant_account_type UNIQUE (tenant, cloud_account_id, type);

ALTER TABLE cloud_resourses drop constraint if exists cloud_resourses_account_resourse_service_type_region_key;
ALTER TABLE cloud_resourses ADD CONSTRAINT cloud_resourses_account_resourse_service_type_region_key UNIQUE (account, resourse_id, type, region, service_name);


alter table cloud_resourses drop constraint if exists cloud_resourses_account_resourse_id_type_region_key;

create table if not exists cloud_account_usage_report (
	id uuid primary key default gen_random_uuid(),
	tenant_id uuid,
	account_id uuid,
	report_date timestamp,
	product_code text,
	service_code text,
	region_code text,
	resource_id text,
	category text,
	sub_category text,
	operation text,
	cost float,
	currency text,
	tags jsonb,
	start_date timestamp,
	end_date timestamp,
	raw jsonb,
	report_inserted_date timestamp,
	resource_type text,
	resource_arn text
);