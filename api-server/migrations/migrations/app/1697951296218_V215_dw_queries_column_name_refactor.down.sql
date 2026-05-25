
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."dw_queries" add column "table_names" text[]
--  null;

alter table "public"."dw_queries" alter column "table_names" drop not null;
alter table "public"."dw_queries" add column "table_names" text;

alter table "public"."dw_queries" rename column "query_normalized_md5" to "query_md5";
