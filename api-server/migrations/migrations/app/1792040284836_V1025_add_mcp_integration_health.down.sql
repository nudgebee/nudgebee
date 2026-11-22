DROP TRIGGER IF EXISTS integration_config_values_reset_health_on_update ON integration_config_values;
DROP TRIGGER IF EXISTS integration_config_values_reset_health_on_change ON integration_config_values;
DROP FUNCTION IF EXISTS fn_integration_config_values_reset_health();
DROP TRIGGER IF EXISTS integrations_reset_health_on_status ON integrations;
DROP FUNCTION IF EXISTS fn_integrations_reset_health_on_status();
ALTER TABLE integrations DROP COLUMN IF EXISTS health;
