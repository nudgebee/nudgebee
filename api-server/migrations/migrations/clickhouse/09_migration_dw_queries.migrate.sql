insert into nudgebee.dw_queries
select *
from postgresql('$CLICKHOUSE_HOST', '$CLICKHOUSE_USER', 'dw_queries', 'postgres', '$CLICKHOUSE_PASSWORD')
SETTINGS memory_overcommit_ratio_denominator=0, memory_overcommit_ratio_denominator_for_user=0;
