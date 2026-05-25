
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- update roles set display_name = 'Admin' where value = 'tenant_admin';

alter table "public"."k8s_workloads" alter column "creation_time" set not null;

alter table "public"."k8s_pods" alter column "node_name" set not null;

alter table "public"."k8s_pods" alter column "creation_time" set not null;


alter table "public"."k8s_pods" alter column "workload_type" set not null;

alter table "public"."k8s_pods" alter column "workload_name" set not null;
