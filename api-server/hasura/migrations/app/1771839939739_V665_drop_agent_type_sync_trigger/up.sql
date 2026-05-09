-- Drop the trigger that overwrites agent.type with cloud_accounts.cloud_provider.
-- This trigger causes two bugs:
-- 1. K8s accounts get agent.type = 'K8s' instead of 'k8s', breaking case-sensitive
--    comparisons in the relay server (which defaults to 'k8s').
-- 2. Proxy agent creation fails with unique constraint violation because the trigger
--    overwrites type='proxy' with the cloud provider name, conflicting with the
--    existing k8s agent for the same account.

DROP TRIGGER IF EXISTS trigger_sync_agent_type_with_cloud_provider ON agent;
DROP FUNCTION IF EXISTS sync_agent_type_with_cloud_provider();

-- For accounts that have BOTH a 'k8s' and 'K8s' agent (duplicates created by the trigger),
-- delete the stale one. Keep the row with the more recent last_connected_at, or if both are
-- NULL / equal, keep the 'k8s' one (original) and delete the 'K8s' one.
DELETE FROM agent WHERE id IN (
    SELECT CASE
        -- K8s row is more recent: delete the k8s row
        WHEN a_upper.last_connected_at > COALESCE(a_lower.last_connected_at, '1970-01-01'::timestamptz)
            THEN a_lower.id
        -- Otherwise: delete the K8s row
        ELSE a_upper.id
    END
    FROM agent a_lower
    JOIN agent a_upper
        ON a_lower.cloud_account_id = a_upper.cloud_account_id
        AND a_lower.tenant = a_upper.tenant
        AND a_upper."type" = 'K8s'
    WHERE a_lower."type" = 'k8s'
);

-- Normalize remaining 'K8s' agent types to 'k8s'
UPDATE agent SET "type" = 'k8s' WHERE "type" = 'K8s';
