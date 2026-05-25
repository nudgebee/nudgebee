
alter table "public"."auto_optimize_resource_map" drop constraint "auto_optimize_resource_map_tenant_id_fkey",
  add constraint "auto_optimize_resource_map_account_id_fkey2"
  foreign key ("account_id")
  references "public"."tenant"
  ("id") on update restrict on delete restrict;

alter table "public"."auto_optimize_resource_map" drop constraint "auto_optimize_resource_map_account_id_fkey2";

alter table "public"."auto_optimize_resource_map" drop constraint "auto_optimize_resource_map_account_id_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_optimize_resource_map" add column "account_id" uuid
--  not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_optimize_resource_map" add column "tenant_id" uuid
--  not null;
