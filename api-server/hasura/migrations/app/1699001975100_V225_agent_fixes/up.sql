

alter table "public"."agent" add column "last_synced_at" timestamp
 null;

CREATE  INDEX "cloud_resource_query_perf_tenant_id_resource_id_group" on
  "public"."dw_queries" using btree ("tenant_id", "account_id", "resource_id", "query_normalized_md5");

CREATE UNIQUE INDEX "agent_access_key" on
  "public"."agent" using btree ("access_key");

CREATE  INDEX "cloud_resource_metrics_resourceid" on
  "public"."cloud_resource_metrics" using btree ("cloud_resource_id");

alter table "public"."agent" add column "version" text
 null;
