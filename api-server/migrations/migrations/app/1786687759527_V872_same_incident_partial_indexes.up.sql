-- Read-path indexes for same-subject incident grouping (#34655): the events
-- list and the auto-analysis child gate both probe event_correlations filtered
-- to correlation_type='same_incident'. The pair+type unique has the type as its
-- LAST column, so those probes can't use it. Partial indexes stay tiny (only
-- same_incident rows) regardless of how large the legacy heuristic's row set is.
CREATE INDEX IF NOT EXISTS idx_event_corr_same_incident_child
  ON event_correlations (event_id, cloud_account_id)
  WHERE correlation_type = 'same_incident';

CREATE INDEX IF NOT EXISTS idx_event_corr_same_incident_leader
  ON event_correlations (related_event_id, cloud_account_id)
  WHERE correlation_type = 'same_incident';
