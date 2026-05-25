set allow_experimental_object_type = 1;
CREATE TABLE IF NOT EXISTS nudgebee.audit_shard on cluster 'default' (
    id UUID DEFAULT generateUUIDv4() NOT NULL,
    user_id UUID NOT NULL,
    tenant_id String DEFAULT '',
    account_id String DEFAULT '',
    event_time DateTime('UTC') DEFAULT now() NOT NULL,
    event_category String NOT NULL,
    event_type String NOT NULL,
    event_prev_state String DEFAULT '',
    event_state String DEFAULT '',
    event_actor String NOT NULL,
    event_target String NOT NULL,
    event_action String NOT NULL,
    event_status String NOT NULL,
    transaction_id String DEFAULT '',
    event_attr String DEFAULT ''
) ENGINE = MergeTree()
ORDER BY(event_category, event_type, event_target, event_action)
PARTITION BY toYYYYMM(event_time);
