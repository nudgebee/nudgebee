insert into integration_types (name, category) values ('clickhouse', 'database') on conflict (name) do nothing;
