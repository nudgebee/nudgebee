
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."integrations_cloud_accounts" add column "default_metrics_provider" boolean
--  not null default 'false';

alter table "public"."integrations_cloud_accounts" alter column "default_log_provider" drop not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."integrations_cloud_accounts" add column "default_log_provider" boolean
--  null default 'false';

alter table "public"."integrations_cloud_accounts" alter column "default_log_provider" drop not null;
alter table "public"."integrations_cloud_accounts" add column "default_log_provider" bool;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."integrations_cloud_accounts" add column "default_traces_provider" boolean
--  not null default 'false';

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."integrations_cloud_accounts" add column "default_log_provider" boolean
--  null;


alter table "public"."integrations_cloud_accounts" drop constraint "integrations_cloud_accounts_integration_id_cloud_account_id_key";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."integrations_cloud_accounts" add column "tenant_id" uuid
--  not null;


DROP TABLE "public"."integrations_cloud_accounts";
