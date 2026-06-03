-- Remove Dynatrace integration type
DELETE FROM integration_types WHERE name IN ('dynatrace');
