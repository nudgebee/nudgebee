CREATE INDEX IF NOT EXISTS idx_recommendation_security_image_by_account
ON recommendation (cloud_account_id, tenant_id)
WHERE category = 'Security'
  AND rule_name = 'image_scan'
  AND status = 'Open'
  AND severity IN ('Critical', 'High')
  AND account_object_id IS NOT NULL;
