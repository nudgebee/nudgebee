CREATE INDEX IF NOT EXISTS idx_rr_filter ON recommendation_resolution (status, resolver_type, type, updated_at DESC);
