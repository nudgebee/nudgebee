-- Revert: Configuration -> RightSizing for policy rules
UPDATE recommendation SET category = 'RightSizing', updated_at = NOW()
WHERE category = 'Configuration'
AND rule_name IN (
  'gcp_bigquery_table_no_expiration',
  'gcp_bigquery_dataset_no_default_expiration',
  'gcp_storage_no_lifecycle'
);

-- Revert: InfraUpgrade -> RightSizing for old resource rules
UPDATE recommendation SET category = 'RightSizing', updated_at = NOW()
WHERE category = 'InfraUpgrade'
AND rule_name IN (
  'gcp_compute_old_instance',
  'gcp_sql_old_instance',
  'gcp_gke_old_cluster',
  'gcp_storage_old_bucket'
);

-- Note: Archived "Cost" duplicates are not restored (stale data from old code)
