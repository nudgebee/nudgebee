create table if not exists integration_categories
(
  value text primary key,
  description text
);

insert into integration_categories(value) values('messaging_queue') on conflict(value) do nothing;
insert into integration_categories(value) values('database') on conflict(value) do nothing;
insert into integration_categories(value) values('log') on conflict(value) do nothing;
insert into integration_categories(value) values('trace') on conflict(value) do nothing;
insert into integration_categories(value) values('metrics') on conflict(value) do nothing;

create table if not exists integration_types
(
	name text primary key,
	category text,
	description text
);

insert into integration_types(name, category) values('rabbitmq', 'messaging_queue') on conflict(name) do nothing;
insert into integration_types(name, category) values('redis', 'database') on conflict(name) do nothing;
insert into integration_types(name, category) values('prometheus', 'metrics') on conflict(name) do nothing;
insert into integration_types(name, category) values('loki', 'log') on conflict(name) do nothing;
insert into integration_types(name, category) values('elastic_search', 'log') on conflict(name) do nothing;
insert into integration_types(name, category) values('postgresql', 'database') on conflict(name) do nothing;
insert into integration_types(name, category) values('mysql', 'database') on conflict(name) do nothing;


create table if not exists integration_sources
(
	value text primary key,
	description text
);

insert into integration_sources(value) values('agent') on conflict(value) do nothing;
insert into integration_sources(value) values('user') on conflict(value) do nothing;

create table if not exists integration_statuses
(
	value text primary key,
	description text
);

insert into integration_statuses(value) values('enabled') on conflict(value) do nothing;
insert into integration_statuses(value) values('error') on conflict(value) do nothing;
insert into integration_statuses(value) values('disabled') on conflict(value) do nothing;

create table if not exists integrations
(
	id uuid primary key default gen_random_uuid(),
	tenant_id uuid not null,
	account_id uuid not null,
	"type" text not null,
	"source" text not null,
	name text not null,
	status text not null,
	created_at timestamp default now(),
	updated_at timestamp default now(),
	created_by uuid,
	updated_by uuid,
	labels json default '{}'
);

create unique index IF NOT exists integrations_account_config_tool_uk on integrations using btree ("type", name, account_id);   

ALTER TABLE integrations DROP CONSTRAINT IF EXISTS integrations_accountid_fkey;
ALTER TABLE integrations ADD CONSTRAINT integrations_accountid_fkey FOREIGN KEY (account_id) REFERENCES cloud_accounts(id);

ALTER TABLE integrations DROP CONSTRAINT IF EXISTS integrations_tenantid_fkey;
ALTER TABLE integrations ADD CONSTRAINT integrations_tenantid_fkey FOREIGN KEY (tenant_id) REFERENCES tenant(id);

ALTER TABLE integrations DROP CONSTRAINT IF EXISTS integrations_type_fkey;
ALTER TABLE integrations ADD CONSTRAINT integrations_type_fkey FOREIGN KEY ("type") REFERENCES integration_types(name);

ALTER TABLE integrations DROP CONSTRAINT IF EXISTS integrations_source_fkey;
ALTER TABLE integrations ADD CONSTRAINT integrations_source_fkey FOREIGN KEY ("source") REFERENCES integration_sources(value);

ALTER TABLE integrations DROP CONSTRAINT IF EXISTS integrations_status_fkey;
ALTER TABLE integrations ADD CONSTRAINT integrations_status_fkey FOREIGN KEY ("status") REFERENCES integration_statuses(value);



create table if not exists integration_config_values
(
	id uuid primary key default gen_random_uuid(),
	integration_id uuid not null,
	name citext,
	value text,
	"type" text,
	is_encrypted bool,
	created_at timestamp default now(),
	updated_at timestamp default now(),
	created_by uuid,
	updated_by uuid
);

create unique index IF NOT exists integration_config_values_config_config_name on integration_config_values using btree (integration_id, name);

ALTER TABLE integration_config_values DROP CONSTRAINT IF EXISTS integration_config_values_integrationid_fkey;
ALTER TABLE integration_config_values ADD CONSTRAINT integration_config_values_integrationid_fkey FOREIGN KEY (integration_id) REFERENCES integrations(id);

