
ALTER TABLE "public"."k8s_nodes" ALTER COLUMN "memory_limits" TYPE double precision;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."k8s_nodes" add column "cpu_limits" float8
--  null;
