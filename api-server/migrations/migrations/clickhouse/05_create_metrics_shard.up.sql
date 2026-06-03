set allow_experimental_object_type = 1;
create table IF NOT EXISTS nudgebee.metrics_shard on cluster 'default'(
  `id` String default generateUUIDv4(),
  `tenant_id` String,
  `cloud_account_id` String,
  `cloud_resource_id` String default '',
  `metric` String,
  `metric_type` String default '',
  `timestamp` DateTime default now(),
  `value` Float64,
  `tags` String default '{}',
)
ENGINE = ReplacingMergeTree
ORDER BY(tenant_id, cloud_account_id, cloud_resource_id, metric, timestamp)
PARTITION BY toYYYYMM(timestamp);
