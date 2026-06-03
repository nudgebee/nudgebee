-- Add zenduty ticketing integration type
INSERT INTO integration_types(name, category, description)
VALUES('zenduty', 'ticketing', 'ZenDuty incident management')
ON CONFLICT(name) DO NOTHING;

-- Add zenduty webhook integration type
INSERT INTO integration_types(name, category, description)
VALUES('zenduty_webhook', 'incident_webhook', 'ZenDuty webhook events')
ON CONFLICT(name) DO NOTHING;

-- Add zenduty_webhook to event sources
INSERT INTO "public"."event_source"("value") VALUES ('zenduty_webhook')
ON CONFLICT DO NOTHING;
