-- Revert same-subject incident grouping links (epic #34655 slice 1).
DELETE FROM event_correlations WHERE correlation_type = 'same_incident';

ALTER TABLE event_correlations DROP CONSTRAINT IF EXISTS "event_ correlation_type";

ALTER TABLE event_correlations ADD CONSTRAINT "event_ correlation_type"
  CHECK (correlation_type = ANY (ARRAY[
    'upstream_dependency',
    'downstream_impact',
    'same_namespace',
    'temporal_proximity',
    'likely_root_cause',
    'same_service',
    'same_resource'
  ]));

-- Restore the pair-level unique: dedupe any pair that acquired rows under more
-- than one correlation_type while the wider key was in place (keep one row).
DELETE FROM event_correlations a
  USING event_correlations b
  WHERE a.related_event_id = b.related_event_id
    AND a.event_id = b.event_id
    AND a.cloud_account_id = b.cloud_account_id
    AND a.ctid > b.ctid;

ALTER TABLE event_correlations
  ADD CONSTRAINT event_correlations_related_event_id_event_id_cloud_account_id_k
  UNIQUE (related_event_id, event_id, cloud_account_id);

ALTER TABLE event_correlations
  DROP CONSTRAINT IF EXISTS event_correlations_pair_account_type_key;
