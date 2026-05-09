-- V639_event_classification_rule_id down.sql
-- Revert: Remove rule_id column from event_classification

DROP INDEX IF EXISTS idx_event_classification_rule_account;
DROP INDEX IF EXISTS idx_event_classification_rule;
ALTER TABLE event_classification DROP COLUMN IF EXISTS rule_id;
