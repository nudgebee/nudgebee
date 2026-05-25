insert into integration_categories (value)
values ('server') on conflict(value) do nothing;

insert into integration_types (category, name)
values ('server', 'ssh') on conflict(name) do nothing;



