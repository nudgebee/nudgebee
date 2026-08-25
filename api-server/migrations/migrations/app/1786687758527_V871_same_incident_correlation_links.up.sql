-- Same-subject incident grouping, slice 1 (epic #34655): `same_incident` links
-- (child event -> group leader) live in event_correlations alongside the legacy
-- heuristic's rows. The legacy unique key is pair-level with no type, so a pair
-- the heuristic already wrote (e.g. same_service) would silently swallow our
-- same_incident link via ON CONFLICT DO NOTHING. Widen the key to pair+type so
-- both rows coexist; neither writer overwrites the other.
--
-- Written idempotently (the constraint swap is guarded) because dev had an
-- earlier numbering of this file applied by hand before main took V867/V868.
-- The DROP names the 63-char form Postgres actually stored (the V605 SQL
-- declared a 66-char identifier, which Postgres truncated on creation).
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'event_correlations_pair_account_type_key'
      AND conrelid = 'event_correlations'::regclass
  ) THEN
    ALTER TABLE event_correlations
      ADD CONSTRAINT event_correlations_pair_account_type_key
      UNIQUE (related_event_id, event_id, cloud_account_id, correlation_type);
  END IF;
END $$;

ALTER TABLE event_correlations
  DROP CONSTRAINT IF EXISTS "event_correlations_related_event_id_event_id_cloud_account_id_k";

ALTER TABLE event_correlations DROP CONSTRAINT IF EXISTS "event_ correlation_type";

ALTER TABLE event_correlations ADD CONSTRAINT "event_ correlation_type"
  CHECK (correlation_type = ANY (ARRAY[
    'upstream_dependency',
    'downstream_impact',
    'same_namespace',
    'temporal_proximity',
    'likely_root_cause',
    'same_service',
    'same_resource',
    'same_incident'
  ]));
