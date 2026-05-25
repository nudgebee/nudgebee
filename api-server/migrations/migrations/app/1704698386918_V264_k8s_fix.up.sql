

alter table "public"."k8s_pods" alter column "workload_name" drop not null;

alter table "public"."k8s_pods" alter column "workload_type" drop not null;

alter table "public"."k8s_pods" alter column "creation_time" drop not null;

alter table "public"."k8s_pods" alter column "node_name" drop not null;

alter table "public"."k8s_workloads" alter column "creation_time" drop not null;

update roles set display_name = 'Admin' where value = 'tenant_admin';
