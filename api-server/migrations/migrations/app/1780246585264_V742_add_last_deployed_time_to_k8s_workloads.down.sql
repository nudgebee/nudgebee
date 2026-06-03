DROP TRIGGER IF EXISTS k8s_workloads_deploy_time_pg ON events;
DROP FUNCTION IF EXISTS fn_k8s_workloads_deploy_time_trigger();
ALTER TABLE k8s_workloads DROP COLUMN IF EXISTS last_deployed_time;
