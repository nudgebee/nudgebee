-- Add last_deployed_time column to k8s_workloads.
-- Populated by a trigger on configuration_change events so the value is
-- a single source of truth for both the KG Workload nodes and the
-- Workloads listing UI.

ALTER TABLE k8s_workloads
    ADD COLUMN IF NOT EXISTS last_deployed_time TIMESTAMPTZ NULL;

-- Trigger: when a configuration_change event is inserted or updated,
-- propagate its starts_at to the matching k8s_workloads row.
-- Uses GREATEST so re-fires of older events can't move the timestamp backwards.
-- IN (subject_name, NULLIF(subject_owner, '')) dedups when the event was
-- recorded against the workload directly (subject_name == subject_owner).
-- Wrapped in EXCEPTION WHEN OTHERS so a workload-update failure never
-- breaks event ingestion (mirrors fn_event_history_trigger in V700).
CREATE OR REPLACE FUNCTION fn_k8s_workloads_deploy_time_trigger()
RETURNS TRIGGER AS $$
BEGIN
    -- Fail-fast guard: skip if tenant/cloud_account_id/starts_at would
    -- broaden the UPDATE to an unbounded scan or cross-tenant rows.
    IF NEW.tenant IS NULL
       OR NEW.cloud_account_id IS NULL
       OR NEW.starts_at IS NULL
       OR NEW.subject_namespace IS NULL
    THEN
        RETURN NEW;
    END IF;

    UPDATE k8s_workloads
    SET    last_deployed_time = GREATEST(
              COALESCE(last_deployed_time, '-infinity'::timestamptz),
              NEW.starts_at
           )
    WHERE  tenant_id        = NEW.tenant
      AND  cloud_account_id = NEW.cloud_account_id
      AND  namespace        = NEW.subject_namespace
      AND  name IN (NEW.subject_name, NULLIF(NEW.subject_owner, ''));

    RETURN NEW;
EXCEPTION WHEN OTHERS THEN
    RAISE WARNING 'k8s_workloads deploy-time trigger failed: %', SQLERRM;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- WHEN predicate keeps the function out of the hot path for non-deploy
-- event UPDATEs (status/priority/ack flows hit events constantly).
-- UPDATE OF narrows the trigger to columns the predicate depends on.
CREATE TRIGGER k8s_workloads_deploy_time_pg
    AFTER INSERT OR UPDATE OF finding_type, starts_at, subject_name, subject_owner, subject_namespace, cloud_account_id, tenant
    ON events
    FOR EACH ROW
    WHEN (NEW.finding_type = 'configuration_change')
    EXECUTE FUNCTION fn_k8s_workloads_deploy_time_trigger();

-- Backfill from historical configuration_change events so existing rows
-- pick up a value at migration time rather than waiting for the next deploy.
-- NULLIF(subject_owner, '') avoids matching workloads named '' when the
-- event has no parent owner.
UPDATE k8s_workloads w
SET    last_deployed_time = sub.max_starts_at
FROM (
    SELECT tenant,
           cloud_account_id,
           subject_namespace,
           COALESCE(NULLIF(subject_owner, ''), subject_name) AS workload_name,
           MAX(starts_at) AS max_starts_at
    FROM   events
    WHERE  finding_type = 'configuration_change'
    GROUP  BY tenant, cloud_account_id, subject_namespace,
              COALESCE(NULLIF(subject_owner, ''), subject_name)
) AS sub
WHERE w.tenant_id        = sub.tenant
  AND w.cloud_account_id = sub.cloud_account_id
  AND w.namespace        = sub.subject_namespace
  AND w.name             = sub.workload_name;
