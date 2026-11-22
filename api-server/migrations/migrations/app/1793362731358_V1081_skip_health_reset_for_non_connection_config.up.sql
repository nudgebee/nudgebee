-- #41887: saving which MCP tools need approval (write_tools) reset the
-- server's health to "Not checked yet". The V1025 trigger cleared health on any
-- config value change, but these keys don't affect the connection:
--   write_tools       which tools ask for approval
--   llm_instructions  text given to Nubi about the integration
-- Every other key still resets health, as before.
--
-- Lock window: CREATE OR REPLACE FUNCTION only; no table lock, no rows rewritten.
-- Numbered V1081 (main is at V1080) so the label does not repeat when test is
-- backmerged to main.

CREATE OR REPLACE FUNCTION fn_integration_config_values_reset_health()
RETURNS TRIGGER AS $$
DECLARE
    target uuid;
BEGIN
    -- Changes to these keys leave health alone. On UPDATE both the old and the new
    -- name must be one of them (a row renamed from url to write_tools still resets),
    -- and the row must stay on the same integration.
    IF TG_OP = 'INSERT' THEN
        IF NEW.name IN ('write_tools', 'llm_instructions') THEN
            RETURN NULL;
        END IF;
    ELSIF TG_OP = 'DELETE' THEN
        IF OLD.name IN ('write_tools', 'llm_instructions') THEN
            RETURN NULL;
        END IF;
    ELSIF TG_OP = 'UPDATE' THEN
        IF OLD.name IN ('write_tools', 'llm_instructions')
           AND NEW.name IN ('write_tools', 'llm_instructions')
           AND OLD.integration_id IS NOT DISTINCT FROM NEW.integration_id THEN
            RETURN NULL;
        END IF;
    END IF;

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
