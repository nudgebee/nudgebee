-- Fix GCP recommendation categories to align with AWS category semantics:
-- Configuration = policy/settings, RightSizing = cost optimization, InfraUpgrade = old resource refresh

-- Step 1: Archive ALL stale "Cost" records for affected rules (from older code)
UPDATE recommendation SET status = 'Archive', updated_at = NOW()
WHERE category = 'Cost'
AND rule_name IN (
  'gcp_bigquery_table_no_expiration',
  'gcp_bigquery_dataset_no_default_expiration',
  'gcp_storage_no_lifecycle'
);

-- Step 2: Archive "RightSizing" records that would conflict with existing "Configuration" records
UPDATE recommendation SET status = 'Archive', updated_at = NOW()
WHERE category = 'RightSizing'
AND rule_name IN (
  'gcp_bigquery_table_no_expiration',
  'gcp_bigquery_dataset_no_default_expiration',
  'gcp_storage_no_lifecycle'
)
AND EXISTS (
  SELECT 1 FROM recommendation r2
  WHERE r2.cloud_account_id = recommendation.cloud_account_id
  AND r2.rule_name = recommendation.rule_name
  AND r2.account_object_id = recommendation.account_object_id
  AND COALESCE(r2.resource_id::text, '') = COALESCE(recommendation.resource_id::text, '')
  AND r2.category = 'Configuration'
);

-- Step 3: Re-categorize remaining RightSizing -> Configuration for policy rules
UPDATE recommendation SET category = 'Configuration', updated_at = NOW()
WHERE category = 'RightSizing'
AND rule_name IN (
  'gcp_bigquery_table_no_expiration',
  'gcp_bigquery_dataset_no_default_expiration',
  'gcp_storage_no_lifecycle'
);

-- Step 4: Archive "RightSizing" records that would conflict with existing "InfraUpgrade" records
UPDATE recommendation SET status = 'Archive', updated_at = NOW()
WHERE category = 'RightSizing'
AND rule_name IN (
  'gcp_compute_old_instance',
  'gcp_sql_old_instance',
  'gcp_gke_old_cluster',
  'gcp_storage_old_bucket'
)
AND EXISTS (
  SELECT 1 FROM recommendation r2
  WHERE r2.cloud_account_id = recommendation.cloud_account_id
  AND r2.rule_name = recommendation.rule_name
  AND r2.account_object_id = recommendation.account_object_id
  AND COALESCE(r2.resource_id::text, '') = COALESCE(recommendation.resource_id::text, '')
  AND r2.category = 'InfraUpgrade'
);

-- Step 5: Re-categorize remaining RightSizing -> InfraUpgrade for old resource rules
UPDATE recommendation SET category = 'InfraUpgrade', updated_at = NOW()
WHERE category = 'RightSizing'
AND rule_name IN (
  'gcp_compute_old_instance',
  'gcp_sql_old_instance',
  'gcp_gke_old_cluster',
  'gcp_storage_old_bucket'
);
