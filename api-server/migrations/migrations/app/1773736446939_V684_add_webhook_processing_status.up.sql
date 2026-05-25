ALTER TABLE event_incoming_webhooks
  ADD COLUMN IF NOT EXISTS processing_status text NOT NULL DEFAULT 'processed';
