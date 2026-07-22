-- Backfill provenance for the system templates seeded by V708/V712 so the bundled
-- and github sources can later supersede them IN PLACE (matched by external_key).
-- source already defaults to 'migration' (V766), so we only assign a stable slug.
-- We intentionally DO NOT touch `source` here: re-running this migration must not
-- clobber rows a sync has already flipped to 'bundled'/'github' (idempotency).

UPDATE workflow_templates
SET external_key = CASE name
      -- V708 (16)
      WHEN 'Rollout Restart Deployment'            THEN 'rollout_restart_deployment'
      WHEN 'Delete Failed Kubernetes Job'          THEN 'delete_failed_kubernetes_job'
      WHEN 'Scale Kubernetes Replicas'             THEN 'scale_kubernetes_replicas'
      WHEN 'Expand Persistent Volume Claim'        THEN 'expand_persistent_volume_claim'
      WHEN 'Cordon and Drain Kubernetes Node'      THEN 'cordon_and_drain_kubernetes_node'
      WHEN 'Patch Container Resource Limits'       THEN 'patch_container_resource_limits'
      WHEN 'Scale RDS Storage'                     THEN 'scale_rds_storage'
      WHEN 'Restart EC2 Instance'                  THEN 'restart_ec2_instance'
      WHEN 'Force ECS Service Deployment'          THEN 'force_ecs_service_deployment'
      WHEN 'Scale Auto Scaling Group'              THEN 'scale_auto_scaling_group'
      WHEN 'Restart Azure Virtual Machine'         THEN 'restart_azure_virtual_machine'
      WHEN 'Expand Azure Managed Disk'             THEN 'expand_azure_managed_disk'
      WHEN 'Restart Azure App Service'             THEN 'restart_azure_app_service'
      WHEN 'Restart GCP VM Instance'               THEN 'restart_gcp_vm_instance'
      WHEN 'Resize GCP Persistent Disk'            THEN 'resize_gcp_persistent_disk'
      WHEN 'Create Ticket from Event'              THEN 'create_ticket_from_event'
      -- V712 (6)
      WHEN 'Force Delete Stuck Terminating Pod'    THEN 'force_delete_stuck_terminating_pod'
      WHEN 'Restart Deployment on High Error Logs' THEN 'restart_deployment_on_high_error_logs'
      WHEN 'Scale RabbitMQ Consumers'              THEN 'scale_rabbitmq_consumers'
      WHEN 'Purge SQS Queue'                       THEN 'purge_sqs_queue'
      WHEN 'Investigate RDS Performance'           THEN 'investigate_rds_performance'
      WHEN 'Restart Cloud SQL Instance'            THEN 'restart_cloud_sql_instance'
      ELSE external_key
    END
WHERE is_system = true AND tenant_id IS NULL AND account_id IS NULL;

-- NOTE: we intentionally do NOT assert that every system template received an
-- external_key. Environments may contain system templates seeded out-of-band (not
-- via V708/V712) — these legitimately fall outside the CASE map and keep a NULL
-- external_key. That is safe: rows with NULL external_key are never superseded by a
-- sync source and are never deactivated by drift reconciliation (DeactivateMissing
-- filters on `external_key IS NOT NULL`). They simply remain unmanaged 'migration'
-- rows until/unless captured into the template repo with a stable slug.
