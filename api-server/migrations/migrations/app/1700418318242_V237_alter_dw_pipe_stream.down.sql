
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."dw_pipe_stream" add column "updated_at" timestamptz
--  null default now();

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."dw_pipe_stream" add column "created_at" timestamptz
--  null default now();

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."dw_pipe_stream" add column "created_by" text
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."dw_pipe_stream" add column "pattern" text
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."dw_pipe_stream" add column "deleted_at" timestamptz
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."dw_pipe_stream" add column "pipe_created_at" timestamptz
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."dw_pipe_stream" add column "pipe_name" text
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."dw_pipe_stream" add column "pipe_id" text
--  null;

alter table "public"."dw_pipe_stream" alter column "bytes_inserted" drop not null;
alter table "public"."dw_pipe_stream" add column "bytes_inserted" numeric;

alter table "public"."dw_pipe_stream" alter column "credit_used" drop not null;
alter table "public"."dw_pipe_stream" add column "credit_used" numeric;
