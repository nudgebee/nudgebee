-- B-tree index for subject_name prefix LIKE queries (subject_name LIKE 'foo%')
CREATE INDEX IF NOT EXISTS idx_events_subject_name
ON events (subject_name text_pattern_ops)
WHERE subject_name IS NOT NULL AND subject_name != '';

-- Functional index for case-insensitive ILIKE prefix queries
CREATE INDEX IF NOT EXISTS idx_events_subject_name_lower
ON events (lower(subject_name) text_pattern_ops)
WHERE subject_name IS NOT NULL AND subject_name != '';
