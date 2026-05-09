ALTER TABLE event_correlations DROP CONSTRAINT "event_ correlation_type";

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
