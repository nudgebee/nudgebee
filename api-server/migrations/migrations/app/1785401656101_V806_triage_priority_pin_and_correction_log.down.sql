DROP TABLE IF EXISTS triage_correction_log;

ALTER TABLE event_triage_rules DROP CONSTRAINT IF EXISTS event_triage_rules_rule_type_check;
ALTER TABLE event_triage_rules
    ADD CONSTRAINT event_triage_rules_rule_type_check
    CHECK (rule_type IN ('suppression', 'scoring', 'classification'));
