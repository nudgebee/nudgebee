UPDATE event_incoming_webhooks SET account_id = '00000000-0000-0000-0000-000000000000' WHERE account_id IS NULL;
ALTER TABLE event_incoming_webhooks ALTER COLUMN account_id SET NOT NULL;
