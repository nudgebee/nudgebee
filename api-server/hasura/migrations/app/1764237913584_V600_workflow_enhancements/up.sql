
alter table workflows
add column if not exists last_execution_time TIMESTAMP WITHOUT TIME ZONE;

insert into integration_types (name, category)
values ('workflow_webhook', 'incident_webhook')
on conflict do nothing;
