-- #39740: the home page image scan card (recommendation_security_groupings_v2,
-- status + severity filtered per running image) read ~116 non-matching rows per
-- image through idx_recommendation_image_scan_account_image_name, which has no
-- status or severity column. This index puts both in the key.
--
-- Pre-build on every live environment BEFORE this migration reaches it, then
-- confirm the build left a valid index (a failed concurrent build leaves an
-- INVALID one, and IF NOT EXISTS below would then silently keep it):
--   SET lock_timeout = '120s'; SET statement_timeout = 0;
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_recommendation_image_scan_acct_img_status_sev
--     ON recommendation (cloud_account_id, tenant_id, (recommendation->>'image_name'), status, severity)
--     WHERE category = 'Security' AND rule_name = 'image_scan' AND account_object_id IS NOT NULL;
--   SELECT indisvalid FROM pg_index WHERE indexrelid = 'idx_recommendation_image_scan_acct_img_status_sev'::regclass;
-- Where it was pre-built this is a no-op. Elsewhere (on-prem) it is a plain
-- build that blocks writes to recommendation while it runs: partial, image-scan
-- rows only; 19 s concurrently on dev for ~1M table rows (8 MB index).
CREATE INDEX IF NOT EXISTS idx_recommendation_image_scan_acct_img_status_sev
  ON recommendation (cloud_account_id, tenant_id, (recommendation->>'image_name'), status, severity)
  WHERE category = 'Security' AND rule_name = 'image_scan' AND account_object_id IS NOT NULL;
