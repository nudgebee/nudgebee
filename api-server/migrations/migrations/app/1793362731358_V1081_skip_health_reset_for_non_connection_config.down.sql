-- Restore the V1025 function: any config value change resets health.
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
