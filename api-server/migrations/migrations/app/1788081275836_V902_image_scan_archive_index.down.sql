-- Restore the V733 index (narrower predicate), then drop the widened one.
-- Build before drop, so the lookup path is never left without an index.
--
-- Same operator note as the up migration: on a large environment pre-apply these
-- CONCURRENTLY by hand first, then this becomes a no-op.

CREATE INDEX IF NOT EXISTS idx_recommendation_security_account_image_name
ON recommendation (cloud_account_id, tenant_id, (recommendation->>'image_name'))
WHERE category = 'Security'
  AND rule_name = 'image_scan'
  AND account_object_id IS NOT NULL;

DROP INDEX IF EXISTS idx_recommendation_image_scan_account_image_name;
