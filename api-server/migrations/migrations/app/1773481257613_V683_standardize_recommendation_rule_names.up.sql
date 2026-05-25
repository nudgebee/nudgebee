-- =====================================================
-- V683: Standardize recommendation rule names
-- =====================================================
-- Merges provider-specific rule names into 18 provider-agnostic
-- groups across AWS/Azure/GCP. Adds cloud_provider column to
-- recommendation_rule for provider-aware metadata lookup.
-- =====================================================

-- =====================================================
-- PART 1: Schema changes to recommendation_rule
-- =====================================================

ALTER TABLE recommendation_rule DROP CONSTRAINT recommendation_rule_metadata_pkey;
ALTER TABLE recommendation_rule ADD COLUMN cloud_provider VARCHAR(10);

-- =====================================================
-- PART 2: Rename & tag merged rule metadata entries
-- Only merged rules get cloud_provider set.
-- Non-merged rules keep cloud_provider = NULL.
-- =====================================================

-- Group 1: vm_underutilized
UPDATE recommendation_rule SET rule_name = 'vm_underutilized', cloud_provider = 'AWS' WHERE rule_name = 'aws_ec2_underutilized';
UPDATE recommendation_rule SET rule_name = 'vm_underutilized', cloud_provider = 'Azure' WHERE rule_name = 'azure_vm_underutilized';
UPDATE recommendation_rule SET rule_name = 'vm_underutilized', cloud_provider = 'GCP' WHERE rule_name = 'gcp_compute_underutilized';

-- Group 2: vm_idle
UPDATE recommendation_rule SET rule_name = 'vm_idle', cloud_provider = 'AWS' WHERE rule_name = 'aws_ec2_idle_instance';
UPDATE recommendation_rule SET rule_name = 'vm_idle', cloud_provider = 'Azure' WHERE rule_name = 'azure_vm_idle_instance';
UPDATE recommendation_rule SET rule_name = 'vm_idle', cloud_provider = 'GCP' WHERE rule_name = 'gcp_compute_idle_instance';

-- Group 3: vm_generation_upgrade
UPDATE recommendation_rule SET rule_name = 'vm_generation_upgrade', cloud_provider = 'AWS' WHERE rule_name = 'aws_ec2_instance_generation_upgrade';
UPDATE recommendation_rule SET rule_name = 'vm_generation_upgrade', cloud_provider = 'Azure' WHERE rule_name = 'azure_vm_generation_upgrade';
UPDATE recommendation_rule SET rule_name = 'vm_generation_upgrade', cloud_provider = 'GCP' WHERE rule_name = 'gcp_compute_generation_upgrade';

-- Group 4: vm_stopped
UPDATE recommendation_rule SET rule_name = 'vm_stopped', cloud_provider = 'AWS' WHERE rule_name = 'aws_ec2_stopped_instance_incurring_storage_cost';
UPDATE recommendation_rule SET rule_name = 'vm_stopped', cloud_provider = 'GCP' WHERE rule_name = 'gcp_compute_stopped_instance';

-- Group 5: missing_tags (multiple Azure & GCP entries — keep one per provider)
UPDATE recommendation_rule SET rule_name = 'missing_tags', cloud_provider = 'AWS' WHERE rule_name = 'aws_tags';
UPDATE recommendation_rule SET rule_name = 'missing_tags', cloud_provider = 'Azure' WHERE rule_name = 'azure_missing_tags';
DELETE FROM recommendation_rule WHERE rule_name IN (
  'azure_storage_missing_tags', 'azure_metric_alert_missing_tags',
  'azure_scheduled_query_rule_missing_tags', 'azure_activity_log_alert_missing_tags',
  'azure_container_app_missing_tags'
);
UPDATE recommendation_rule SET rule_name = 'missing_tags', cloud_provider = 'GCP' WHERE rule_name = 'gcp_compute_no_labels';
DELETE FROM recommendation_rule WHERE rule_name IN (
  'gcp_storage_no_labels', 'gcp_sql_no_labels', 'gcp_bigquery_dataset_no_labels',
  'gcp_bigquery_table_no_labels', 'gcp_gke_no_labels', 'gcp_function_no_labels',
  'gcp_run_no_labels'
);

-- Group 6: orphaned_volume
UPDATE recommendation_rule SET rule_name = 'orphaned_volume', cloud_provider = 'AWS' WHERE rule_name = 'aws_ec2_orphaned_volume';
UPDATE recommendation_rule SET rule_name = 'orphaned_volume', cloud_provider = 'Azure' WHERE rule_name = 'azure_disk_unattached_volume';

-- Group 7: storage_public_access
UPDATE recommendation_rule SET rule_name = 'storage_public_access', cloud_provider = 'AWS' WHERE rule_name = 'aws_s3_public_access_acl';
UPDATE recommendation_rule SET rule_name = 'storage_public_access', cloud_provider = 'Azure' WHERE rule_name = 'azure_storage_blob_public_access_enabled';
UPDATE recommendation_rule SET rule_name = 'storage_public_access', cloud_provider = 'GCP' WHERE rule_name = 'gcp_storage_public_access';

-- Group 8: storage_versioning_disabled
UPDATE recommendation_rule SET rule_name = 'storage_versioning_disabled', cloud_provider = 'AWS' WHERE rule_name = 'aws_s3_versioning';
UPDATE recommendation_rule SET rule_name = 'storage_versioning_disabled', cloud_provider = 'Azure' WHERE rule_name = 'azure_storage_versioning_disabled';
UPDATE recommendation_rule SET rule_name = 'storage_versioning_disabled', cloud_provider = 'GCP' WHERE rule_name = 'gcp_storage_no_versioning';

-- Group 9: storage_no_lifecycle
UPDATE recommendation_rule SET rule_name = 'storage_no_lifecycle', cloud_provider = 'AWS' WHERE rule_name = 'aws_s3_lifecycle';
UPDATE recommendation_rule SET rule_name = 'storage_no_lifecycle', cloud_provider = 'GCP' WHERE rule_name = 'gcp_storage_no_lifecycle';

-- Group 10: storage_no_cmek
UPDATE recommendation_rule SET rule_name = 'storage_no_cmek', cloud_provider = 'Azure' WHERE rule_name = 'azure_storage_cmk_disabled';
UPDATE recommendation_rule SET rule_name = 'storage_no_cmek', cloud_provider = 'GCP' WHERE rule_name = 'gcp_storage_no_cmek';

-- Group 11: storage_class_optimization
UPDATE recommendation_rule SET rule_name = 'storage_class_optimization', cloud_provider = 'Azure' WHERE rule_name = 'azure_storage_access_tier_optimization';
UPDATE recommendation_rule SET rule_name = 'storage_class_optimization', cloud_provider = 'GCP' WHERE rule_name = 'gcp_storage_class_optimization';

-- Group 12: db_backup_disabled (3 Azure entries — keep one)
UPDATE recommendation_rule SET rule_name = 'db_backup_disabled', cloud_provider = 'AWS' WHERE rule_name = 'aws_rds_backup_enabled';
UPDATE recommendation_rule SET rule_name = 'db_backup_disabled', cloud_provider = 'Azure' WHERE rule_name = 'azure_mysql_backup_disabled';
DELETE FROM recommendation_rule WHERE rule_name IN ('azure_postgres_backup_disabled', 'azure_mariadb_backup_disabled');
UPDATE recommendation_rule SET rule_name = 'db_backup_disabled', cloud_provider = 'GCP' WHERE rule_name = 'gcp_sql_no_backup';

-- Group 13: db_public_access
UPDATE recommendation_rule SET rule_name = 'db_public_access', cloud_provider = 'AWS' WHERE rule_name = 'aws_rds_public_access';
UPDATE recommendation_rule SET rule_name = 'db_public_access', cloud_provider = 'Azure' WHERE rule_name = 'azure_sql_public_network_access_enabled';

-- Group 14: db_storage_autoscaling
UPDATE recommendation_rule SET rule_name = 'db_storage_autoscaling', cloud_provider = 'AWS' WHERE rule_name = 'aws_rds_storage_autoscaling';
UPDATE recommendation_rule SET rule_name = 'db_storage_autoscaling', cloud_provider = 'Azure' WHERE rule_name = 'azure_sql_storage_auto_growth_disabled';

-- Group 15: k8s_logging_disabled
UPDATE recommendation_rule SET rule_name = 'k8s_logging_disabled', cloud_provider = 'AWS' WHERE rule_name = 'aws_eks_logging';
UPDATE recommendation_rule SET rule_name = 'k8s_logging_disabled', cloud_provider = 'GCP' WHERE rule_name = 'gcp_gke_logging_disabled';

-- Group 16: k8s_network_policy
UPDATE recommendation_rule SET rule_name = 'k8s_network_policy', cloud_provider = 'Azure' WHERE rule_name = 'azure_aks_network_policy_disabled';
UPDATE recommendation_rule SET rule_name = 'k8s_network_policy', cloud_provider = 'GCP' WHERE rule_name = 'gcp_gke_no_network_policy';

-- Group 17: unused_load_balancer
UPDATE recommendation_rule SET rule_name = 'unused_load_balancer', cloud_provider = 'AWS' WHERE rule_name = 'aws_elb_unused';
UPDATE recommendation_rule SET rule_name = 'unused_load_balancer', cloud_provider = 'Azure' WHERE rule_name = 'azure_unused_load_balancer';

-- Group 18: unassociated_public_ip
UPDATE recommendation_rule SET rule_name = 'unassociated_public_ip', cloud_provider = 'AWS' WHERE rule_name = 'aws_vpc_unallocated_elastic_ip';
UPDATE recommendation_rule SET rule_name = 'unassociated_public_ip', cloud_provider = 'Azure' WHERE rule_name = 'azure_unassociated_public_ip';

-- =====================================================
-- PART 3: Insert generic (NULL cloud_provider) rows
-- for merged rules — used when browsing/listing
-- =====================================================

INSERT INTO recommendation_rule (rule_name, cloud_provider, category, title, description, service_name, recommendations, mitigations, compliances, "references")
VALUES
  ('vm_underutilized', NULL, 'RightSizing', 'Underutilized VM Instance',
   'This virtual machine appears to be underutilized based on CPU and memory metrics. Consider downsizing to a smaller instance type to reduce costs while maintaining adequate performance.', '',
   '["Review the instance utilization metrics and consider downsizing to a smaller instance type that better matches actual workload requirements."]'::jsonb,
   '[]'::jsonb, '[]'::jsonb, '[]'::jsonb),
  ('vm_idle', NULL, 'RightSizing', 'Idle VM Instance',
   'This virtual machine shows minimal or no activity over the monitoring period. An idle instance continues to incur compute costs without providing value.', '',
   '["Investigate why the instance is idle. If no longer needed, terminate it. If needed intermittently, consider using auto-scaling or scheduled start/stop."]'::jsonb,
   '[]'::jsonb, '[]'::jsonb, '[]'::jsonb),
  ('vm_generation_upgrade', NULL, 'InfraUpgrade', 'VM Generation Upgrade Available',
   'This virtual machine is running on an older hardware generation. Newer generations typically offer better price-performance.', '',
   '["Upgrade to the latest instance generation for improved performance and cost efficiency."]'::jsonb,
   '[]'::jsonb, '[]'::jsonb, '[]'::jsonb),
  ('vm_stopped', NULL, 'RightSizing', 'Stopped VM Instance',
   'This virtual machine is in a stopped state but still incurs storage costs for attached volumes. Consider terminating if no longer needed.', '',
   '["If the instance is no longer needed, terminate it and delete associated volumes to eliminate ongoing storage costs."]'::jsonb,
   '[]'::jsonb, '[]'::jsonb, '[]'::jsonb),
  ('missing_tags', NULL, 'Configuration', 'Missing Tags/Labels',
   'This resource does not have proper tags or labels configured. Tags help with cost allocation, access control, and resource organization.', '',
   '["Add appropriate tags or labels to this resource for better organization, cost tracking, and access management."]'::jsonb,
   '[]'::jsonb, '["APRA", "MAS", "NIST4"]'::jsonb, '[]'::jsonb),
  ('orphaned_volume', NULL, 'RightSizing', 'Orphaned Volume',
   'This storage volume is not attached to any virtual machine. Unattached volumes incur storage costs without providing value.', '',
   '["Snapshot the volume if data needs to be preserved, then delete the unattached volume to eliminate unnecessary storage costs."]'::jsonb,
   '[]'::jsonb, '[]'::jsonb, '[]'::jsonb),
  ('storage_public_access', NULL, 'Configuration', 'Storage Public Access Enabled',
   'This storage resource has public access enabled, which could expose sensitive data to unauthorized users.', '',
   '["Review the public access configuration and restrict access to only authorized users and applications."]'::jsonb,
   '[]'::jsonb, '[]'::jsonb, '[]'::jsonb),
  ('storage_versioning_disabled', NULL, 'Configuration', 'Storage Versioning Disabled',
   'Object versioning is not enabled for this storage resource. Versioning provides data protection against accidental deletion or overwrites.', '',
   '["Enable versioning to protect against accidental data deletion or modification."]'::jsonb,
   '[]'::jsonb, '[]'::jsonb, '[]'::jsonb),
  ('storage_no_lifecycle', NULL, 'Configuration', 'Storage Lifecycle Not Configured',
   'This storage resource does not have lifecycle management rules configured. Lifecycle policies help optimize costs by automatically transitioning or expiring objects.', '',
   '["Configure lifecycle rules to automatically manage object storage classes and expiration based on access patterns."]'::jsonb,
   '[]'::jsonb, '[]'::jsonb, '[]'::jsonb),
  ('storage_no_cmek', NULL, 'Configuration', 'Storage Not Using Customer-Managed Key',
   'This storage resource is not encrypted with a customer-managed encryption key (CMEK). Using CMEK provides additional control over data encryption.', '',
   '["Configure customer-managed encryption keys for enhanced data security and compliance."]'::jsonb,
   '[]'::jsonb, '[]'::jsonb, '[]'::jsonb),
  ('storage_class_optimization', NULL, 'RightSizing', 'Storage Class Optimization',
   'This storage resource may benefit from a different storage class based on its access patterns. Optimizing storage class can reduce costs.', '',
   '["Review access patterns and consider moving to a more cost-effective storage class."]'::jsonb,
   '[]'::jsonb, '[]'::jsonb, '[]'::jsonb),
  ('db_backup_disabled', NULL, 'Configuration', 'Database Backup Disabled',
   'Automated backups are not properly configured for this database instance. Regular backups are essential for data protection and disaster recovery.', '',
   '["Enable automated backups with an appropriate retention period to ensure data protection."]'::jsonb,
   '[]'::jsonb, '[]'::jsonb, '[]'::jsonb),
  ('db_public_access', NULL, 'Configuration', 'Database Public Access Enabled',
   'This database instance has public network access enabled, which increases the attack surface and risk of unauthorized access.', '',
   '["Disable public network access and use private endpoints or VPN for database connectivity."]'::jsonb,
   '[]'::jsonb, '[]'::jsonb, '[]'::jsonb),
  ('db_storage_autoscaling', NULL, 'Configuration', 'Database Storage Autoscaling Disabled',
   'Storage autoscaling is not enabled for this database instance. Without autoscaling, the database may run out of storage during peak usage.', '',
   '["Enable storage autoscaling to ensure the database can handle storage growth automatically."]'::jsonb,
   '[]'::jsonb, '[]'::jsonb, '[]'::jsonb),
  ('k8s_logging_disabled', NULL, 'Configuration', 'Kubernetes Logging Disabled',
   'Cluster logging is not enabled for this Kubernetes cluster. Logging is essential for security monitoring, troubleshooting, and compliance.', '',
   '["Enable cluster logging to capture API server, audit, and controller manager logs for security and operational visibility."]'::jsonb,
   '[]'::jsonb, '[]'::jsonb, '[]'::jsonb),
  ('k8s_network_policy', NULL, 'Configuration', 'Kubernetes Network Policy Disabled',
   'Network policies are not enforced in this Kubernetes cluster. Without network policies, all pods can communicate freely, increasing security risk.', '',
   '["Enable network policy enforcement and define policies to restrict pod-to-pod communication based on the principle of least privilege."]'::jsonb,
   '[]'::jsonb, '[]'::jsonb, '[]'::jsonb),
  ('unused_load_balancer', NULL, 'RightSizing', 'Unused Load Balancer',
   'This load balancer has no healthy backend targets registered. It continues to incur costs without distributing any traffic.', '',
   '["Remove unused load balancers to eliminate unnecessary costs, or register appropriate backend targets."]'::jsonb,
   '[]'::jsonb, '[]'::jsonb, '[]'::jsonb),
  ('unassociated_public_ip', NULL, 'RightSizing', 'Unassociated Public IP',
   'This public IP address is not associated with any resource. Unassociated public IPs incur costs without providing value.', '',
   '["Release unassociated public IP addresses to reduce costs, or associate them with the appropriate resources."]'::jsonb,
   '[]'::jsonb, '[]'::jsonb, '[]'::jsonb);

-- =====================================================
-- PART 4: Create unique index
-- =====================================================

CREATE UNIQUE INDEX idx_recommendation_rule_name_provider
  ON recommendation_rule (rule_name, COALESCE(cloud_provider, ''));

-- =====================================================
-- PART 5: Rename rule_names in recommendation table
-- =====================================================
-- Multiple old rule names map to a single new name. The collector may
-- already be writing the new names, so existing rows with the new name
-- must also participate in deduplication. We include self-mappings
-- (new_name → new_name) so the dedup window covers both old and
-- already-migrated rows. After dedup, only old-name rows are renamed.

-- Step 1: Deduplicate across all groups (old names + existing new names)
DELETE FROM recommendation WHERE id IN (
  SELECT id FROM (
    SELECT r.id, ROW_NUMBER() OVER (
      PARTITION BY m.new_name, r.category, r.cloud_account_id, r.resource_id, r.account_object_id
      ORDER BY r.updated_at DESC
    ) as rn
    FROM recommendation r
    JOIN (VALUES
      -- self-mappings: include already-migrated rows in dedup window
      ('vm_underutilized', 'vm_underutilized'),
      ('vm_idle', 'vm_idle'),
      ('vm_generation_upgrade', 'vm_generation_upgrade'),
      ('vm_stopped', 'vm_stopped'),
      ('missing_tags', 'missing_tags'),
      ('orphaned_volume', 'orphaned_volume'),
      ('storage_public_access', 'storage_public_access'),
      ('storage_versioning_disabled', 'storage_versioning_disabled'),
      ('storage_no_lifecycle', 'storage_no_lifecycle'),
      ('storage_no_cmek', 'storage_no_cmek'),
      ('storage_class_optimization', 'storage_class_optimization'),
      ('db_backup_disabled', 'db_backup_disabled'),
      ('db_public_access', 'db_public_access'),
      ('db_storage_autoscaling', 'db_storage_autoscaling'),
      ('k8s_logging_disabled', 'k8s_logging_disabled'),
      ('k8s_network_policy', 'k8s_network_policy'),
      ('unused_load_balancer', 'unused_load_balancer'),
      ('unassociated_public_ip', 'unassociated_public_ip'),
      -- old → new mappings
      ('aws_ec2_underutilized', 'vm_underutilized'),
      ('azure_vm_underutilized', 'vm_underutilized'),
      ('gcp_compute_underutilized', 'vm_underutilized'),
      ('aws_ec2_idle_instance', 'vm_idle'),
      ('azure_vm_idle_instance', 'vm_idle'),
      ('gcp_compute_idle_instance', 'vm_idle'),
      ('aws_ec2_instance_generation_upgrade', 'vm_generation_upgrade'),
      ('azure_vm_generation_upgrade', 'vm_generation_upgrade'),
      ('gcp_compute_generation_upgrade', 'vm_generation_upgrade'),
      ('aws_ec2_stopped_instance_incurring_storage_cost', 'vm_stopped'),
      ('gcp_compute_stopped_instance', 'vm_stopped'),
      ('aws_tags', 'missing_tags'),
      ('azure_missing_tags', 'missing_tags'),
      ('azure_storage_missing_tags', 'missing_tags'),
      ('azure_metric_alert_missing_tags', 'missing_tags'),
      ('azure_scheduled_query_rule_missing_tags', 'missing_tags'),
      ('azure_activity_log_alert_missing_tags', 'missing_tags'),
      ('azure_container_app_missing_tags', 'missing_tags'),
      ('gcp_compute_no_labels', 'missing_tags'),
      ('gcp_sql_no_labels', 'missing_tags'),
      ('gcp_bigquery_dataset_no_labels', 'missing_tags'),
      ('gcp_bigquery_table_no_labels', 'missing_tags'),
      ('gcp_gke_no_labels', 'missing_tags'),
      ('gcp_storage_no_labels', 'missing_tags'),
      ('gcp_function_no_labels', 'missing_tags'),
      ('gcp_run_no_labels', 'missing_tags'),
      ('aws_ec2_orphaned_volume', 'orphaned_volume'),
      ('azure_disk_unattached_volume', 'orphaned_volume'),
      ('aws_s3_public_access_acl', 'storage_public_access'),
      ('azure_storage_blob_public_access_enabled', 'storage_public_access'),
      ('gcp_storage_public_access', 'storage_public_access'),
      ('aws_s3_versioning', 'storage_versioning_disabled'),
      ('azure_storage_versioning_disabled', 'storage_versioning_disabled'),
      ('gcp_storage_no_versioning', 'storage_versioning_disabled'),
      ('aws_s3_lifecycle', 'storage_no_lifecycle'),
      ('gcp_storage_no_lifecycle', 'storage_no_lifecycle'),
      ('azure_storage_cmk_disabled', 'storage_no_cmek'),
      ('gcp_storage_no_cmek', 'storage_no_cmek'),
      ('azure_storage_access_tier_optimization', 'storage_class_optimization'),
      ('gcp_storage_class_optimization', 'storage_class_optimization'),
      ('aws_rds_backup_enabled', 'db_backup_disabled'),
      ('azure_mysql_backup_disabled', 'db_backup_disabled'),
      ('azure_postgres_backup_disabled', 'db_backup_disabled'),
      ('azure_mariadb_backup_disabled', 'db_backup_disabled'),
      ('gcp_sql_no_backup', 'db_backup_disabled'),
      ('aws_rds_public_access', 'db_public_access'),
      ('azure_sql_public_network_access_enabled', 'db_public_access'),
      ('aws_rds_storage_autoscaling', 'db_storage_autoscaling'),
      ('azure_sql_storage_auto_growth_disabled', 'db_storage_autoscaling'),
      ('aws_eks_logging', 'k8s_logging_disabled'),
      ('gcp_gke_logging_disabled', 'k8s_logging_disabled'),
      ('azure_aks_network_policy_disabled', 'k8s_network_policy'),
      ('gcp_gke_no_network_policy', 'k8s_network_policy'),
      ('aws_elb_unused', 'unused_load_balancer'),
      ('azure_unused_load_balancer', 'unused_load_balancer'),
      ('aws_vpc_unallocated_elastic_ip', 'unassociated_public_ip'),
      ('azure_unassociated_public_ip', 'unassociated_public_ip')
    ) AS m(old_name, new_name) ON r.rule_name = m.old_name
  ) ranked WHERE rn > 1
);

-- Step 2: Rename remaining old-name rows (survivors from dedup)
UPDATE recommendation r SET rule_name = m.new_name
FROM (VALUES
  ('aws_ec2_underutilized', 'vm_underutilized'),
  ('azure_vm_underutilized', 'vm_underutilized'),
  ('gcp_compute_underutilized', 'vm_underutilized'),
  ('aws_ec2_idle_instance', 'vm_idle'),
  ('azure_vm_idle_instance', 'vm_idle'),
  ('gcp_compute_idle_instance', 'vm_idle'),
  ('aws_ec2_instance_generation_upgrade', 'vm_generation_upgrade'),
  ('azure_vm_generation_upgrade', 'vm_generation_upgrade'),
  ('gcp_compute_generation_upgrade', 'vm_generation_upgrade'),
  ('aws_ec2_stopped_instance_incurring_storage_cost', 'vm_stopped'),
  ('gcp_compute_stopped_instance', 'vm_stopped'),
  ('aws_tags', 'missing_tags'),
  ('azure_missing_tags', 'missing_tags'),
  ('azure_storage_missing_tags', 'missing_tags'),
  ('azure_metric_alert_missing_tags', 'missing_tags'),
  ('azure_scheduled_query_rule_missing_tags', 'missing_tags'),
  ('azure_activity_log_alert_missing_tags', 'missing_tags'),
  ('azure_container_app_missing_tags', 'missing_tags'),
  ('gcp_compute_no_labels', 'missing_tags'),
  ('gcp_sql_no_labels', 'missing_tags'),
  ('gcp_bigquery_dataset_no_labels', 'missing_tags'),
  ('gcp_bigquery_table_no_labels', 'missing_tags'),
  ('gcp_gke_no_labels', 'missing_tags'),
  ('gcp_storage_no_labels', 'missing_tags'),
  ('gcp_function_no_labels', 'missing_tags'),
  ('gcp_run_no_labels', 'missing_tags'),
  ('aws_ec2_orphaned_volume', 'orphaned_volume'),
  ('azure_disk_unattached_volume', 'orphaned_volume'),
  ('aws_s3_public_access_acl', 'storage_public_access'),
  ('azure_storage_blob_public_access_enabled', 'storage_public_access'),
  ('gcp_storage_public_access', 'storage_public_access'),
  ('aws_s3_versioning', 'storage_versioning_disabled'),
  ('azure_storage_versioning_disabled', 'storage_versioning_disabled'),
  ('gcp_storage_no_versioning', 'storage_versioning_disabled'),
  ('aws_s3_lifecycle', 'storage_no_lifecycle'),
  ('gcp_storage_no_lifecycle', 'storage_no_lifecycle'),
  ('azure_storage_cmk_disabled', 'storage_no_cmek'),
  ('gcp_storage_no_cmek', 'storage_no_cmek'),
  ('azure_storage_access_tier_optimization', 'storage_class_optimization'),
  ('gcp_storage_class_optimization', 'storage_class_optimization'),
  ('aws_rds_backup_enabled', 'db_backup_disabled'),
  ('azure_mysql_backup_disabled', 'db_backup_disabled'),
  ('azure_postgres_backup_disabled', 'db_backup_disabled'),
  ('azure_mariadb_backup_disabled', 'db_backup_disabled'),
  ('gcp_sql_no_backup', 'db_backup_disabled'),
  ('aws_rds_public_access', 'db_public_access'),
  ('azure_sql_public_network_access_enabled', 'db_public_access'),
  ('aws_rds_storage_autoscaling', 'db_storage_autoscaling'),
  ('azure_sql_storage_auto_growth_disabled', 'db_storage_autoscaling'),
  ('aws_eks_logging', 'k8s_logging_disabled'),
  ('gcp_gke_logging_disabled', 'k8s_logging_disabled'),
  ('azure_aks_network_policy_disabled', 'k8s_network_policy'),
  ('gcp_gke_no_network_policy', 'k8s_network_policy'),
  ('aws_elb_unused', 'unused_load_balancer'),
  ('azure_unused_load_balancer', 'unused_load_balancer'),
  ('aws_vpc_unallocated_elastic_ip', 'unassociated_public_ip'),
  ('azure_unassociated_public_ip', 'unassociated_public_ip')
) AS m(old_name, new_name)
WHERE r.rule_name = m.old_name;

-- =====================================================
-- PART 6: Fix typo renames in recommendation table
-- =====================================================
-- Same dedup-then-rename pattern: the target name may already exist.

DELETE FROM recommendation WHERE id IN (
  SELECT id FROM (
    SELECT r.id, ROW_NUMBER() OVER (
      PARTITION BY m.new_name, r.category, r.cloud_account_id, r.resource_id, r.account_object_id
      ORDER BY r.updated_at DESC
    ) as rn
    FROM recommendation r
    JOIN (VALUES
      ('azure_vm_backups_disabled', 'azure_vm_backup_disabled'),
      ('azure_vm_backup_disabled', 'azure_vm_backup_disabled'),
      ('aws_native_co_ec2_rightsize', 'aws_native_rightsize'),
      ('aws_native_rightsize', 'aws_native_rightsize')
    ) AS m(old_name, new_name) ON r.rule_name = m.old_name
  ) ranked WHERE rn > 1
);

UPDATE recommendation SET rule_name = 'azure_vm_backup_disabled'
  WHERE rule_name = 'azure_vm_backups_disabled';

UPDATE recommendation SET rule_name = 'aws_native_rightsize'
  WHERE rule_name = 'aws_native_co_ec2_rightsize';

-- =====================================================
-- PART 7: Fix categories in recommendation table
-- =====================================================
-- 56 GCP resources have missing_tags with BOTH Cost and Configuration
-- categories (from old GCP rules). Delete Cost rows where a Configuration
-- row exists for the same resource, then rename remaining Cost rows.

-- missing_tags: dedup across Cost and Configuration categories, keeping one
-- row per resource (prefer Configuration, then most recently updated), then
-- rename the survivor to Configuration.
DELETE FROM recommendation WHERE id IN (
  SELECT id FROM (
    SELECT r.id, ROW_NUMBER() OVER (
      PARTITION BY r.cloud_account_id, r.resource_id, r.account_object_id
      ORDER BY (CASE WHEN r.category = 'Configuration' THEN 0 ELSE 1 END), r.updated_at DESC
    ) as rn
    FROM recommendation r
    WHERE r.rule_name = 'missing_tags'
    AND r.category IN ('Configuration', 'Cost')
  ) ranked WHERE rn > 1
);
UPDATE recommendation SET category = 'Configuration'
  WHERE rule_name = 'missing_tags' AND category = 'Cost';

-- orphaned_volume: dedup across InfraUpgrade and RightSizing categories,
-- keeping one row per resource (prefer RightSizing, then most recently
-- updated), then rename the survivor to RightSizing.
DELETE FROM recommendation WHERE id IN (
  SELECT id FROM (
    SELECT r.id, ROW_NUMBER() OVER (
      PARTITION BY r.cloud_account_id, r.resource_id, r.account_object_id
      ORDER BY (CASE WHEN r.category = 'RightSizing' THEN 0 ELSE 1 END), r.updated_at DESC
    ) as rn
    FROM recommendation r
    WHERE r.rule_name = 'orphaned_volume'
    AND r.category IN ('RightSizing', 'InfraUpgrade')
  ) ranked WHERE rn > 1
);
UPDATE recommendation SET category = 'RightSizing'
  WHERE rule_name = 'orphaned_volume' AND category = 'InfraUpgrade';

-- azure_vm_accelerated_networking_disabled: some resources have rows across
-- Configuration, InfraUpgrade, and RightSizing categories. Dedup to keep one
-- row per resource (prefer Configuration, then most recently updated), then
-- rename the survivor to Configuration.
DELETE FROM recommendation WHERE id IN (
  SELECT id FROM (
    SELECT r.id, ROW_NUMBER() OVER (
      PARTITION BY r.cloud_account_id, r.resource_id, r.account_object_id
      ORDER BY (CASE WHEN r.category = 'Configuration' THEN 0 ELSE 1 END), r.updated_at DESC
    ) as rn
    FROM recommendation r
    WHERE r.rule_name = 'azure_vm_accelerated_networking_disabled'
    AND r.category IN ('Configuration', 'InfraUpgrade', 'RightSizing')
  ) ranked WHERE rn > 1
);
UPDATE recommendation SET category = 'Configuration'
  WHERE rule_name = 'azure_vm_accelerated_networking_disabled'
  AND category IN ('InfraUpgrade', 'RightSizing');
