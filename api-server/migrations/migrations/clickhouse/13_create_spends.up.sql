set allow_experimental_object_type = 1;
create table if not exists nudgebee.spends 
    on cluster 'default' 
    as nudgebee.spends_shard 
    ENGINE = Distributed('default', 'nudgebee', 'spends_shard', murmurHash3_64(tenant, cloud_account, cloud_resource_id, date));
