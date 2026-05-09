CREATE TABLE IF NOT EXISTS "public"."event_triage_rule_matches" (
    "id" uuid NOT NULL DEFAULT gen_random_uuid(),
    "event_id" uuid NOT NULL,
    "rule_id" uuid NOT NULL,
    "cloud_account_id" uuid NOT NULL,
    "tenant_id" uuid NOT NULL,
    "rule_type" text NOT NULL,
    "action" text NOT NULL,
    "matched_at" timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY ("id"),
    FOREIGN KEY ("event_id") REFERENCES "public"."events"("id") ON DELETE CASCADE,
    FOREIGN KEY ("rule_id") REFERENCES "public"."event_triage_rules"("id") ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_etrm_rule_tenant ON "public"."event_triage_rule_matches"(rule_id, tenant_id, matched_at DESC);
CREATE INDEX IF NOT EXISTS idx_etrm_event ON "public"."event_triage_rule_matches"(event_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_etrm_event_rule ON "public"."event_triage_rule_matches"(event_id, rule_id, cloud_account_id);
