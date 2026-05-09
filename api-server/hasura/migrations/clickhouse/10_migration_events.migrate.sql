insert into nudgebee.events
select *
from postgresql('$CLICKHOUSE_HOST', '$CLICKHOUSE_USER', 'events', 'postgres', '$CLICKHOUSE_PASSWORD')
