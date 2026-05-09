set allow_experimental_object_type = 1;
CREATE TABLE if not exists nudgebee.spends_shard on cluster 'default'(
	id String default generateUUIDv4(),
	date Datetime default now(),
	amount Float64,
	unit String default 'USD',
	business_unit String default '',
	tenant String,
	cloud_account String,
	cloud_resource_id String default '',
	exclude_aggregate boolean default false,
	tags String default '{}',
)
ENGINE = ReplacingMergeTree
ORDER BY(tenant, cloud_account, cloud_resource_id, date)
PARTITION BY toYYYYMM(date);
