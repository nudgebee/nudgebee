-- Restoring the old key can fail if rules of the same name now exist under two
-- sources (exactly what the up migration enables). Deduplicate before rolling
-- back: keep the non-webhook definition row, drop the webhook-sourced twin.
DELETE FROM event_rules a
USING event_rules b
WHERE a.account_id = b.account_id
  AND a.tenant_id = b.tenant_id
  AND a.alert = b.alert
  AND a.id <> b.id
  AND a.source LIKE '%\_webhook'
  AND b.source NOT LIKE '%\_webhook';

ALTER TABLE event_rules
    ADD CONSTRAINT event_rules_account_id_tenant_id_alert_key
    UNIQUE (account_id, tenant_id, alert);

DROP INDEX IF EXISTS event_rules_account_tenant_source_alert_key;
