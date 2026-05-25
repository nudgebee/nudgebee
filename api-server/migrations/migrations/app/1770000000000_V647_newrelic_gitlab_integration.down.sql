-- Remove New Relic integration types
DELETE FROM integration_types WHERE name IN ('newrelic', 'newrelic_webhook', 'gitlab');
