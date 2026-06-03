
alter table "public"."dw_pipe_usage" drop constraint "dw_pipe_usage_cloud_account_id_tenant_id_pipe_id_start_at_end_at_key";

alter table "public"."dw_pipe_usage" add constraint "dw_pipe_usage_tenant_id_cloud_account_id_pipe_name_pipe_id_key" unique ("tenant_id", "cloud_account_id", "pipe_name", "pipe_id");

alter table "public"."dw_pipe_usage" rename column "bytes_inserted" to "bytes_migrated";

DROP TABLE "public"."dw_pipe";

DROP TABLE "public"."dw_pipe_usage";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP table "public"."dw_pipe_stream";
