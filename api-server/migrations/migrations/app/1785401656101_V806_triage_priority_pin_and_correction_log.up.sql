-- Human-override support for triage scoring.
--
-- 1) Allow a new rule_type 'priority_pin' on event_triage_rules: an ABSOLUTE human
--    correction that pins the priority (and optionally the verdict category/intrinsic) for
--    matched events, overriding the LLM verdict and suppressing additive scoring rules.
--    Reuses the existing match/scope/effective_from-until/override machinery.
ALTER TABLE event_triage_rules DROP CONSTRAINT IF EXISTS event_triage_rules_rule_type_check;
ALTER TABLE event_triage_rules
    ADD CONSTRAINT event_triage_rules_rule_type_check
    CHECK (rule_type IN ('suppression', 'scoring', 'classification', 'priority_pin'));

-- 2) Append-only provenance: the tamper-evident source of truth for "who set this score and
--    why". score_factors on events stays reconstructable from this; it is never the authority.
CREATE TABLE IF NOT EXISTS triage_correction_log (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid NOT NULL,
    cloud_account_id uuid,
    -- target: a single event, or a whole signal class
    event_id      uuid,
    class_key     text,
    -- what changed
    old_priority  text,
    new_priority  text,
    old_score     integer,
    new_score     integer,
    -- provenance
    source        text NOT NULL,             -- human_event_pin | human_class_pin | rule | llm | system
    authority     text,                      -- human:event | human:class | machine
    rule_id       uuid,                       -- the event_triage_rules row, when applicable
    corrected_by  uuid,
    reason        text,
    -- model context at the time
    prompt_version text,
    model          text,
    created_at    timestamp without time zone NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_triage_correction_log_event ON triage_correction_log (event_id);
CREATE INDEX IF NOT EXISTS idx_triage_correction_log_class ON triage_correction_log (tenant_id, class_key);
