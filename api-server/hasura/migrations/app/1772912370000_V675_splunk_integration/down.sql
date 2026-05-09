-- Remove Splunk integration types
DELETE FROM integration_types WHERE name IN ('splunk', 'splunk_webhook');
