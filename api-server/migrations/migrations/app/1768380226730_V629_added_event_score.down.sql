-- Drop constraints
ALTER TABLE events DROP CONSTRAINT IF EXISTS chk_computed_priority_values;
ALTER TABLE events DROP CONSTRAINT IF EXISTS chk_computed_score_range;

-- Drop indexes
DROP INDEX IF EXISTS idx_events_computed_score;
DROP INDEX IF EXISTS idx_events_computed_priority;

-- Drop columns
ALTER TABLE events DROP COLUMN IF EXISTS score_confidence;
ALTER TABLE events DROP COLUMN IF EXISTS score_factors;
ALTER TABLE events DROP COLUMN IF EXISTS computed_priority;
ALTER TABLE events DROP COLUMN IF EXISTS computed_score;
