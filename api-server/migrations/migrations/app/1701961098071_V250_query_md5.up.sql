
alter table "public"."dw_queries" add column "query_md5" text
 null;

CREATE  INDEX "cloud_resource_query_perf_querymd5" on
  "public"."dw_queries" using btree ("query_md5");
