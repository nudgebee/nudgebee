
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE TRIGGER set_public_k8s_pods_updated_at
--     BEFORE UPDATE ON k8s_pods
--     FOR EACH ROW
--     EXECUTE FUNCTION set_current_timestamp_updated_at();

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."k8s_pods" add column "updated_at" timestamp
--  null default now();

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE TRIGGER set_public_k8s_workloads_updated_at
--     BEFORE UPDATE ON k8s_workloads
--     FOR EACH ROW
--     EXECUTE FUNCTION set_current_timestamp_updated_at();

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."k8s_workloads" add column "updated_at" timestamp
--  null default now();

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE TRIGGER set_public_k8s_namespaces_updated_at
--     BEFORE UPDATE ON k8s_namespaces
--     FOR EACH ROW
--     EXECUTE FUNCTION set_current_timestamp_updated_at();

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."k8s_namespaces" add column "updated_at" timestamp
--  null default now();

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE TRIGGER set_public_k8s_nodes_updated_at
--     BEFORE UPDATE ON k8s_nodes
--     FOR EACH ROW
--     EXECUTE FUNCTION set_current_timestamp_updated_at();
