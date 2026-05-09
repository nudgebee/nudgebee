
alter table "public"."dw_queries" rename column "query_md5" to "query_normalized_md5";

alter table "public"."dw_queries" drop column "table_names" cascade;

alter table "public"."dw_queries" add column "table_names" text[]
 null;
