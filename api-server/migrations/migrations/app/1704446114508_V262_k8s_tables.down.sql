
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."spends" add column "tags" jsonb
--  null;


ALTER TABLE "public"."k8s_nodes" ALTER COLUMN "cpu_allocatable" TYPE integer;

ALTER TABLE "public"."k8s_nodes" ALTER COLUMN "cpu_capacity" TYPE integer;

ALTER TABLE "public"."k8s_nodes" ALTER COLUMN "cpu_capacity" TYPE double precision;

ALTER TABLE "public"."k8s_nodes" ALTER COLUMN "cpu_capacity" TYPE integer;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."k8s_nodes" add column "taints" text
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."k8s_nodes" add column "labels" jsonb
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."k8s_nodes" add column "internal_ip" text
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."k8s_nodes" add column "external_ip" text
--  null;

alter table "public"."k8s_namespaces" rename to "k8s_namespace";

DROP TABLE "public"."k8s_namespace";

DROP TABLE "public"."k8s_nodes";

DROP TABLE "public"."k8s_workloads";

DROP TABLE "public"."k8s_pods";
