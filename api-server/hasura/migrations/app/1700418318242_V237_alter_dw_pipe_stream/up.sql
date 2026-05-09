
alter table "public"."dw_pipe_stream" drop column "credit_used" cascade;

alter table "public"."dw_pipe_stream" drop column "bytes_inserted" cascade;

alter table "public"."dw_pipe_stream" add column "pipe_id" text
 null;

alter table "public"."dw_pipe_stream" add column "pipe_name" text
 null;

alter table "public"."dw_pipe_stream" add column "pipe_created_at" timestamptz
 null;

alter table "public"."dw_pipe_stream" add column "deleted_at" timestamptz
 null;

alter table "public"."dw_pipe_stream" add column "pattern" text
 null;

alter table "public"."dw_pipe_stream" add column "created_by" text
 null;

alter table "public"."dw_pipe_stream" add column "created_at" timestamptz
 null default now();

alter table "public"."dw_pipe_stream" add column "updated_at" timestamptz
 null default now();
