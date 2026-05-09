set allow_experimental_object_type = 1;
CREATE TABLE IF NOT EXISTS nudgebee.audit
    on cluster 'default'
    as nudgebee.audit_shard
    ENGINE = Distributed('default', 'nudgebee', 'audit_shard', murmurHash3_64(event_time));