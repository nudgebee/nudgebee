
alter table event_log_analysis add column if not exists status_reason text;

alter table event_log_analysis add column if not exists cloud_account_id uuid;

alter table event_log_analysis add column if not exists event_aggregation_key text;

insert into event_log_analysis_status (value, description) values ('FAILED','Failed') on conflict(value) do nothing;

update event_log_analysis ela set event_fingerprint = e.fingerprint, cloud_account_id = e.cloud_account_id, event_aggregation_key = e.aggregation_key  FROM events AS e WHERE e.id = ela.event_id;

WITH RowNumCTE AS (
    SELECT cloud_account_id, event_fingerprint, event_aggregation_key, 
           ROW_NUMBER() OVER (PARTITION BY cloud_account_id, event_fingerprint, event_aggregation_key ORDER BY cloud_account_id, event_fingerprint, event_aggregation_key) AS rn
    FROM event_log_analysis
)
DELETE FROM event_log_analysis
WHERE (cloud_account_id, event_fingerprint, event_aggregation_key) IN (SELECT cloud_account_id, event_fingerprint, event_aggregation_key FROM RowNumCTE WHERE rn > 1);

alter table event_log_analysis drop constraint if exists event_log_analysis_fingerprint_aggregationkey_accountid;

alter table event_log_analysis add constraint event_log_analysis_fingerprint_aggregationkey_accountid unique (cloud_account_id, event_fingerprint, event_aggregation_key);

ALTER TABLE event_log_analysis ALTER COLUMN recorded_at TYPE timestamp;