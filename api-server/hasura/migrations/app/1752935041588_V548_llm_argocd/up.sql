insert into integration_categories 
values ('ci_cd') on conflict(value) do nothing;

insert into integration_types(name, category)
values ('argocd','ci_cd') on conflict (name) do nothing;


