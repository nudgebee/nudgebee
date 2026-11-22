-- #40994: MCP integration health.
--
-- integrations.health holds, per scope ("*" for a direct integration, the
-- account id for a vm_agent one), the last verdict llm-server reached on the
-- path Nubi uses: {"status","reason","tool_count","last_checked_at",
-- "failing_since","last_probe_at"}. NULL means not checked yet. llm-server is
-- the only writer of a verdict (tools/core/mcp_integration_health.go);
-- integrations.status stays the user's on/off switch.
--
-- The triggers below only ever reset health to NULL, so every path that
-- re-enables an integration or changes its config starts it over, whichever
-- service does it (api-server has three status writers and two save paths;
-- relay-server rewrites agent config values on every reconnect).
--
-- Lock window: ADD COLUMN without a default is a catalog-only change; the
-- ACCESS EXCLUSIVE lock on integrations lasts milliseconds. CREATE TRIGGER
-- takes SHARE ROW EXCLUSIVE on integrations and integration_config_values,
-- also briefly. No rows are rewritten.

ALTER TABLE integrations ADD COLUMN IF NOT EXISTS health jsonb;

-- The user enabled or disabled the integration, through any path.
CREATE OR REPLACE FUNCTION fn_integrations_reset_health_on_status()
RETURNS TRIGGER AS $$
BEGIN
    NEW.health := NULL;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS integrations_reset_health_on_status ON integrations;
-- The WHEN guard matters: the k8s-collector heartbeat rewrites status on
-- every message without changing it.
CREATE TRIGGER integrations_reset_health_on_status
    BEFORE UPDATE OF status ON integrations
    FOR EACH ROW
    WHEN (OLD.status IS DISTINCT FROM NEW.status)
    EXECUTE FUNCTION fn_integrations_reset_health_on_status();

-- A config value was added, changed or removed.
CREATE OR REPLACE FUNCTION fn_integration_config_values_reset_health()
RETURNS TRIGGER AS $$
DECLARE
    target uuid;
BEGIN
    IF TG_OP = 'DELETE' THEN
        target := OLD.integration_id;
    ELSE
        target := NEW.integration_id;
    END IF;
    UPDATE integrations SET health = NULL WHERE id = target AND health IS NOT NULL;
    -- A value moved to another integration changes both.
    IF TG_OP = 'UPDATE' AND OLD.integration_id IS DISTINCT FROM NEW.integration_id THEN
        UPDATE integrations SET health = NULL WHERE id = OLD.integration_id AND health IS NOT NULL;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS integration_config_values_reset_health_on_change ON integration_config_values;
CREATE TRIGGER integration_config_values_reset_health_on_change
    AFTER INSERT OR DELETE ON integration_config_values
    FOR EACH ROW
    EXECUTE FUNCTION fn_integration_config_values_reset_health();

DROP TRIGGER IF EXISTS integration_config_values_reset_health_on_update ON integration_config_values;
-- The WHEN guard matters: relay-server upserts connection_mode / agent_type /
-- datasource_key with their unchanged values on every agent reconnect.
CREATE TRIGGER integration_config_values_reset_health_on_update
    AFTER UPDATE ON integration_config_values
    FOR EACH ROW
    WHEN (OLD.value IS DISTINCT FROM NEW.value OR OLD.integration_id IS DISTINCT FROM NEW.integration_id)
    EXECUTE FUNCTION fn_integration_config_values_reset_health();
