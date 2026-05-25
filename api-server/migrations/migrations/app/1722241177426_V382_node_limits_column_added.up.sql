
alter table "public"."k8s_nodes" add column IF NOT EXISTS  "cpu_limits" float8 null;

alter table "public"."k8s_nodes" add column IF NOT EXISTS  "memory_limits" float8 null;

ALTER TABLE "public"."k8s_nodes" ALTER COLUMN "memory_limits" TYPE int4;
