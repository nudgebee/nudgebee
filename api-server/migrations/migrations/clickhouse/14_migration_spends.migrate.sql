set allow_experimental_object_type = 1;
insert into nudgebee.spends(id, date, amount, unit, business_unit, tenant, cloud_account, cloud_resource_id, exclude_aggregate, tags)
select id, date, amount, if(unit is null, 'USD', unit), if(business_unit is null, '', toString(business_unit)), tenant, cloud_account, if(cloud_resource_id is null, '', toString(cloud_resource_id)), exclude_aggregate, assumeNotNull(if (tags is null, '{}', tags))
from postgresql('$CLICKHOUSE_HOST', '$CLICKHOUSE_USER', 'spends', 'postgres', '$CLICKHOUSE_PASSWORD');
