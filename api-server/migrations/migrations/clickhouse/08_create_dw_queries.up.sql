set allow_experimental_object_type = 1;
create table  IF NOT EXISTS nudgebee.dw_queries 
on cluster 'default' 
as nudgebee.dw_queries_shard 
ENGINE = Distributed('default', 'nudgebee', 'dw_queries_shard', murmurHash3_64(tenant_id, account_id, resource_id, query_id, database_name, db_username, query_md5));
