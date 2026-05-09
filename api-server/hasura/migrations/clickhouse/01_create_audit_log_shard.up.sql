CREATE TABLE if not exists nudgebee.agent_audit_log_shard on cluster 'default'  (
	id String default generateUUIDv4(),
	created_at Datetime default now(),
	updated_at Datetime default now(),
	url String,
	client_ip String,
	status_code Int64 default 0,
	headers String,
	agent_id String default '',
	tenant_id String default '',
	cloud_account_id String default '',
	"method" String,
	time_taken UInt64,
)
ENGINE = MergeTree
ORDER BY(tenant_id, cloud_account_id, agent_id)
PARTITION BY toYYYYMM(created_at);
