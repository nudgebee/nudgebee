-- Recreate the trigger and function (for rollback only)
CREATE OR REPLACE FUNCTION sync_agent_type_with_cloud_provider()
RETURNS TRIGGER AS $$
BEGIN
    SELECT cloud_provider INTO NEW.type
    FROM cloud_accounts
    WHERE id = NEW.cloud_account_id;

    IF NEW.type IS NULL THEN
        NEW.type := OLD.type;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_sync_agent_type_with_cloud_provider
    BEFORE INSERT OR UPDATE ON agent
    FOR EACH ROW
    EXECUTE FUNCTION sync_agent_type_with_cloud_provider();

-- Revert normalized types back to cloud_provider values
UPDATE agent a
SET "type" = ca.cloud_provider
FROM cloud_accounts ca
WHERE a.cloud_account_id = ca.id;
