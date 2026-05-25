-- V639_event_classification_rule_id up.sql
-- Add rule_id column to event_classification for fast drilldown queries

-- Add rule_id column to track which triage rule created the classification
ALTER TABLE event_classification
ADD COLUMN IF NOT EXISTS rule_id UUID REFERENCES event_triage_rules(id) ON DELETE SET NULL;

-- Index for fast drilldown queries (events by rule_id)
CREATE INDEX IF NOT EXISTS idx_event_classification_rule
ON event_classification(rule_id, classified_at DESC)
WHERE rule_id IS NOT NULL;

-- Index for account-scoped rule drilldown
CREATE INDEX IF NOT EXISTS idx_event_classification_rule_account
ON event_classification(rule_id, cloud_account_id, classified_at DESC)
WHERE rule_id IS NOT NULL;
