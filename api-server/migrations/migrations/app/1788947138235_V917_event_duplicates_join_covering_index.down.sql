-- Reverts V917. Drops the covering index (whether it arrived via this
-- migration or the out-of-band CONCURRENTLY step). The unique index
-- event_duplicates_event_id_cloud_account_id_key still backs the join keys;
-- event_groupings_v2 returns to the pre-V917 heap-fetch behaviour.

DROP INDEX IF EXISTS idx_event_dup_evt_acct_cover;
