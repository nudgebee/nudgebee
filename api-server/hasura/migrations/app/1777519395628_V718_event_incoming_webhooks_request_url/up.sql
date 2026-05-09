-- Persist the original request URL so async (ProcessStoredWebhook) and
-- replay (ReplayWebhookEvent) paths can re-extract URL query params and apply
-- the same label merge as the synchronous router.
ALTER TABLE event_incoming_webhooks
  ADD COLUMN IF NOT EXISTS request_url TEXT;
