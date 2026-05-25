insert into event_source (value) values ('AWS_EventBridge') on conflict (value) do nothing;
insert into event_source (value) values ('datadog_webhook') on conflict (value) do nothing;
insert into integration_categories (value) values ('observability_platform') on conflict (value) do nothing;
insert into integration_types (name, category) values ('datadog', 'observability_platform') on conflict (name) do nothing;
insert into integration_types (name, category) values ('datadog_webhook', 'incident_webhook') on conflict (name) do nothing;
