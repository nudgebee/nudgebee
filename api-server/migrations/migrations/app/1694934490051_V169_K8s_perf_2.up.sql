
create unique index if not exists cloudaccount_k8s_metrics_aggregate_pk on cloudaccount_k8s_metrics_aggregate (tenant_id, account_id, timestamp, metric);

create unique index if not exists cloudaccount_k8s_aggregate_pk on cloudaccount_k8s_aggregate (tenant_id, account_id);

create unique index if not exists cloudaccount_k8s_node_aggregate_pk on cloudaccount_k8s_node_aggregate (tenant_id, account_id, node_name, timestamp);

create unique index if not exists cloudaccount_k8s_pod_aggregate_pk on cloudaccount_k8s_pod_aggregate (tenant_id, account_id, namespace_name, pod_name, timestamp);

create unique index if not exists cloudaccount_k8s_workload_aggregate_pk on cloudaccount_k8s_workload_aggregate (tenant_id, account_id, namespace_name, workload_name, timestamp);
