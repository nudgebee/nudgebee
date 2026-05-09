
alter table "public"."k8s_namespaces" add column "workload_count" integer
 null;

alter table "public"."k8s_namespaces" add column "creation_time" timestamp
 null;

alter table "public"."k8s_namespaces" add column "pod_count" integer
 null;
