ALTER TABLE events
ADD COLUMN IF NOT EXISTS computed_score INTEGER,
ADD COLUMN IF NOT EXISTS computed_priority TEXT,
ADD COLUMN IF NOT EXISTS score_factors JSONB,
ADD COLUMN IF NOT EXISTS score_confidence DECIMAL(3, 2);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_events_computed_priority 
ON events (computed_priority) WHERE computed_priority IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_events_computed_score 
ON events (computed_score DESC) WHERE computed_score IS NOT NULL;

-- Constraints
ALTER TABLE events
ADD CONSTRAINT chk_computed_score_range 
CHECK (computed_score IS NULL OR (computed_score >= 0 AND computed_score <= 100));

ALTER TABLE events
ADD CONSTRAINT chk_computed_priority_values 
CHECK (computed_priority IS NULL OR computed_priority IN ('P0', 'P1', 'P2', 'P3'));
