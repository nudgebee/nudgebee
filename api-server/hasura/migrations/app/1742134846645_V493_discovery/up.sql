drop table if exists cloud_resource_job_schedule_events;
drop table if exists cloud_resource_job_schedule;
drop table if exists cloud_resource_job_schedule_status_type;
drop table if exists cloud_resource_job_schedule_event_status_type;
drop table if exists cloud_resource_job_action_type;


create table if not exists cloud_resource_attributes(
	id uuid DEFAULT gen_random_uuid() NOT null,
	tenant_id uuid NOT null,
	account_id uuid not null,
	resource_id uuid not null,
	name varchar(256) not null,
	value text,
	labels json default '{}',
	created_at timestamp default now(),
	last_seen_at timestamp default now(),
	CONSTRAINT cloudresourceattributes_pkey PRIMARY KEY (id),
	CONSTRAINT cloudresourceattributes_resourceid_source UNIQUE (resource_id, name),
	CONSTRAINT cloudresourceattributes_tenant_fk FOREIGN KEY (tenant_id) REFERENCES public.tenant(id),
	CONSTRAINT cloudresourceattributes_resourceid_fk FOREIGN KEY (resource_id) REFERENCES public.cloud_resourses(id)
);