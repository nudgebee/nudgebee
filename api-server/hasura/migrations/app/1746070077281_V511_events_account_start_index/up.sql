drop index if exists events_account_starts_brin_idx;
create index events_account_starts_brin_idx on events using brin (cloud_account_id, starts_at);