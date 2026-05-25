

create or replace view k8s_account_resource_usage as 
select tenant_id, cloud_account_id
    , sum(case when metric = 'cpuCoreRequestAverage' then value end) as avg_cpu_request
    , sum(case when metric = 'cpuCoreUsageAverage' then value end) as avg_cpu_usage
    , sum(case when metric = 'ramByteRequestAverage' then value/
    (1024*1024) end) as avg_mem_request
    , sum(case when metric = 'ramByteUsageAverage' then value/
    (1024*1024) end) as avg_mem_usage
    , sum(case when metric = 'networkReceiveBytes' then value/
    (1024*1024) end) as avg_ingress
    , sum(case when metric = 'networkTransferBytes' then value/
    (1024*1024) end) as avg_egress
from
(
    select tenant_id, cloud_account_id, cloud_resource_id, "timestamp", metric, value, row_number () OVER (
            PARTITION BY cloud_account_id, cloud_resource_id, metric
    		ORDER BY "timestamp" DESC NULLS LAST
    	) rank
    from cloud_resource_metrics
    where cloud_resource_id in (select cloud_resource_id from k8s_nodes where is_active = true)
) n
where n.rank = 1
group by n.tenant_id, n.cloud_account_id;



DROP VIEW IF EXISTS "public"."compliance_account_execution";

DROP VIEW IF EXISTS "public"."compliance_check_findings_count_aggregate";

DROP VIEW IF EXISTS "public"."spends_account_date_aggregate";

DROP VIEW IF EXISTS "public"."spends_account_monthly_aggregate";

DROP VIEW IF EXISTS "public"."spends_amount_sum_daily_aggregate";

DROP VIEW IF EXISTS "public"."spends_project_aggregate";

DROP VIEW IF EXISTS "public"."spends_project_monthly_aggregate";

DROP FUNCTION IF EXISTS "public"."cloud_account_service_groupings"("pg_catalog"."_text", "pg_catalog"."json", "pg_catalog"."json");

DROP VIEW IF EXISTS "public"."cloud_account_service_groupings_type";

DROP FUNCTION IF EXISTS "public"."cloud_resource_k8s_pod_groupings"("pg_catalog"."_text", "pg_catalog"."json", "pg_catalog"."json");

DROP VIEW IF EXISTS "public"."cloud_resource_k8s_pod_groupings_type";

DROP MATERIALIZED VIEW IF EXISTS "public"."cloudaccount_k8s_resource_aggregate";

DROP MATERIALIZED VIEW IF EXISTS "public"."cloudaccount_k8s_workload_aggregate";

DROP MATERIALIZED VIEW IF EXISTS "public"."cloudaccount_k8s_namespace_aggregate";

DROP MATERIALIZED VIEW IF EXISTS "public"."cloudaccount_k8s_security_recommendation";

DROP MATERIALIZED VIEW IF EXISTS "public"."cloudaccount_k8s_aggregate";

DROP MATERIALIZED VIEW IF EXISTS "public"."cloudaccount_k8s_resource_workload_aggregate";

DROP MATERIALIZED VIEW IF EXISTS "public"."cloudaccount_k8s_resource_namespace_aggregate";

DROP MATERIALIZED VIEW IF EXISTS "public"."cloudaccount_k8s_resource_score_aggregate";

DROP MATERIALIZED VIEW IF EXISTS "public"."cloudaccount_k8s_pod_aggregate";

DROP MATERIALIZED VIEW IF EXISTS "public"."cloudaccount_k8s_node_aggregate";

drop materialized view if exists cloudaccount_k8s_resource_node_aggregate;

alter table "public"."k8s_nodes"
  add constraint "k8s_nodes_cloud_account_id_fkey"
  foreign key ("cloud_account_id")
  references "public"."cloud_accounts"
  ("id") on update restrict on delete restrict;

alter table "public"."k8s_pods"
  add constraint "k8s_pods_cloud_account_id_fkey"
  foreign key ("cloud_account_id")
  references "public"."cloud_accounts"
  ("id") on update restrict on delete restrict;
