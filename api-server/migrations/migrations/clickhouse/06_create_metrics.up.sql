set allow_experimental_object_type = 1;
create table IF NOT EXISTS nudgebee.metrics
on cluster 'default'
as nudgebee.metrics_shard
ENGINE = Distributed('default', 'nudgebee', 'metrics_shard', murmurHash3_64(tenant_id, cloud_account_id, cloud_resource_id, metric));
