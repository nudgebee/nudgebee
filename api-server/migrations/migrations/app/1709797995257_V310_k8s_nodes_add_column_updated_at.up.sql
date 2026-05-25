
alter table "public"."k8s_nodes" add column "updated_at" timestamp
 null default now();
