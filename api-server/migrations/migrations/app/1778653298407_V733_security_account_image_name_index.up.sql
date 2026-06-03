CREATE INDEX IF NOT EXISTS idx_recommendation_security_account_image_name
ON recommendation (cloud_account_id, tenant_id, (recommendation->>'image_name'))
WHERE category = 'Security'
  AND rule_name = 'image_scan'
  AND account_object_id IS NOT NULL;
