-- Re-key event_rules from (account_id, tenant_id, alert) to
-- (account_id, tenant_id, source, alert).
--
-- Why: rules polled from an external provider (Datadog monitors, Grafana alert
-- rules, …) must be able to coexist with the webhook-ingested row of the same
-- name. Under the old key a synced definition and its firing webhook collapsed
-- onto one row, so the sync silently took ownership of webhook-created rows.
--
-- Safe to apply: (account_id, tenant_id, source, alert) is strictly weaker than
-- the key it replaces, so no existing row can violate it.
--
-- Deliberately NOT built CONCURRENTLY: every migration runs inside a
-- transaction here, so CREATE INDEX CONCURRENTLY fails with
-- "cannot run inside a transaction block" (and CI rejects it outright).
-- event_rules is a small configuration table, so the brief ACCESS EXCLUSIVE
-- lock is acceptable. On a deployment where it is not, build the index by hand
-- with CREATE UNIQUE INDEX CONCURRENTLY before rolling this out — the
-- IF NOT EXISTS below then makes this statement a no-op.
--
-- ON CONFLICT targets a unique *index* just as well as a constraint, so the
-- index alone is enough for upsertEventRule.
CREATE UNIQUE INDEX IF NOT EXISTS event_rules_account_tenant_source_alert_key
    ON event_rules (account_id, tenant_id, source, alert);

-- Dropped only after the replacement exists. NOTE: api-server pods running the
-- previous image upsert with ON CONFLICT (account_id, tenant_id, alert); they
-- will error until the new image is rolled out, so this migration is not
-- backward compatible with the running replica set.
ALTER TABLE event_rules DROP CONSTRAINT IF EXISTS event_rules_account_id_tenant_id_alert_key;
