-- Remove zenduty_webhook from event sources
DELETE FROM "public"."event_source" WHERE "value" = 'zenduty_webhook';

-- Remove zenduty webhook integration type
DELETE FROM integration_types WHERE name = 'zenduty_webhook';

-- Remove zenduty ticketing integration type
DELETE FROM integration_types WHERE name = 'zenduty';
