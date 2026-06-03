
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_query_perf" add column "query_ended_at" timestamp
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_query_perf" add column "query_started_at" timestamp
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_query_perf" add column "query_result_cache_hit" boolean
--  null default 'false';

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_query_perf" add column "query_status" text
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_query_perf" add column "query_session_id" text
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_query_perf" add column "query_transaction_id" text
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_query_perf" add column "query_usage_limit" text
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_query_perf" add column "query_returned_bytes" integer
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_query_perf" add column "query_returned_rows" integer
--  null default '0';

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_query_perf" add column "query_error_message" text
--  null;

alter table "public"."cloud_resource_query_perf" rename column "query_planning_duration_milli" to "query_planning_time_milli";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_query_perf" add column "query_planning_time_milli" integer
--  not null default '0';

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_query_perf" add column "query_text" text
--  not null;

alter table "public"."cloud_resource_query_perf" alter column "query_total_exec_duration_milli" drop not null;
alter table "public"."cloud_resource_query_perf" add column "query_total_exec_duration_milli" int4;
