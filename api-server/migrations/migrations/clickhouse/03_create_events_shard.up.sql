CREATE TABLE IF NOT EXISTS nudgebee.events_shard on cluster 'default' (
	id String default generateUUIDv4(),
	created_at Datetime default now(),
	updated_at Datetime default now(),
	finding_id String,
	title String,
	description String DEFAULT '',
	"source" String DEFAULT '',
	aggregation_key String,
	failure String DEFAULT '',
	finding_type String,
	category String DEFAULT '',
	priority String,
	subject_type String DEFAULT '',
	subject_name String DEFAULT '',
	subject_namespace String DEFAULT '',
	subject_node String DEFAULT '',
	service_key String DEFAULT '',
	"cluster" String,
	ends_at Datetime DEFAULT '1970-01-01',
	starts_at Datetime DEFAULT '1970-01-01',
	fingerprint String DEFAULT '',
	evidences String DEFAULT '{}',
	tenant String,
	cloud_account_id String,
	cloud_resource_id String DEFAULT '',
	status String DEFAULT ''
)
ENGINE = ReplacingMergeTree
ORDER BY(tenant, cloud_account_id, cloud_resource_id, finding_id)
PARTITION BY toYYYYMM(created_at);