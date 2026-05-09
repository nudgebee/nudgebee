ALTER TABLE recommendation
  ADD COLUMN IF NOT EXISTS finops_score INTEGER,
  ADD COLUMN IF NOT EXISTS finops_band TEXT,
  ADD COLUMN IF NOT EXISTS finops_score_breakdown JSONB;

CREATE INDEX idx_recommendation_finops_score ON recommendation (finops_score DESC NULLS LAST) WHERE status = 'Open';
CREATE INDEX idx_recommendation_finops_band ON recommendation (finops_band) WHERE status = 'Open';
