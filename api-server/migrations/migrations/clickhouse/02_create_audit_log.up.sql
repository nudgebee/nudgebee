create table if not exists nudgebee.agent_audit_log
on cluster 'default' 
as nudgebee.agent_audit_log_shard
ENGINE = Distributed('default', 'nudgebee', 'agent_audit_log_shard', murmurHash3_64(tenant_id, cloud_account_id, agent_id));
