CREATE INDEX IF NOT EXISTS idx_etrm_rule_account
    ON "public"."event_triage_rule_matches"(rule_id, cloud_account_id);
