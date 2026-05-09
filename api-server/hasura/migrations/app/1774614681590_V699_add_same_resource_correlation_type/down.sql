ALTER TABLE event_correlations DROP CONSTRAINT "event_ correlation_type";

-- Remap any rows using the removed type before re-adding the stricter constraint
UPDATE event_correlations SET correlation_type = 'temporal_proximity' WHERE correlation_type = 'same_resource';

ALTER TABLE event_correlations ADD CONSTRAINT "event_ correlation_type"
  CHECK (correlation_type = ANY (ARRAY[
    'upstream_dependency',
    'downstream_impact',
    'same_namespace',
    'temporal_proximity',
    'likely_root_cause',
    'same_service'
  ]));
