insert into nudgebee.metrics(id, tenant_id, cloud_account_id, cloud_resource_id, metric, `timestamp`, value, tags)
select id, tenant_id, cloud_account_id, cloud_resource_id, metric, `timestamp`, value, tags::text
from postgresql('$CLICKHOUSE_HOST', '$CLICKHOUSE_USER', 'cloud_resource_metrics', 'postgres', '$CLICKHOUSE_PASSWORD');
