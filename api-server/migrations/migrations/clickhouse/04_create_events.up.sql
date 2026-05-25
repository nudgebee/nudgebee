create table IF NOT EXISTS nudgebee.events
on cluster 'default' 
as nudgebee.events_shard
ENGINE = Distributed('default', 'nudgebee', 'events_shard', murmurHash3_64(tenant, cloud_account_id, cloud_resource_id, finding_id));