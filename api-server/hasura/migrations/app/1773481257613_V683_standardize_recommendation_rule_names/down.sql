-- Reverse schema changes only.
-- Rule name renames in the recommendation table are NOT reversed
-- as the collector code will continue writing agnostic names.

DROP INDEX IF EXISTS idx_recommendation_rule_name_provider;

-- Delete generic rows for merged rules
DELETE FROM recommendation_rule WHERE cloud_provider IS NULL AND rule_name IN (
  'vm_underutilized', 'vm_idle', 'vm_generation_upgrade', 'vm_stopped',
  'missing_tags', 'orphaned_volume',
  'storage_public_access', 'storage_versioning_disabled', 'storage_no_lifecycle',
  'storage_no_cmek', 'storage_class_optimization',
  'db_backup_disabled', 'db_public_access', 'db_storage_autoscaling',
  'k8s_logging_disabled', 'k8s_network_policy',
  'unused_load_balancer', 'unassociated_public_ip'
);

ALTER TABLE recommendation_rule DROP COLUMN cloud_provider;

-- Restore original primary key
ALTER TABLE recommendation_rule ADD PRIMARY KEY (rule_name);
