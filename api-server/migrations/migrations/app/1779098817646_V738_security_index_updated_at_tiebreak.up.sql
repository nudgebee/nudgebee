-- Create security recommendation index with updated_at DESC as tiebreak.
-- Column order: (cloud_account_id, status, severity_weight DESC, updated_at DESC)
-- Within the same severity_weight (e.g. all Critical=10), the planner returns
-- the most recently scanned CVEs first — those are more likely to have an active
-- pod and therefore non-null namespace/workload_name in the App column.
DROP INDEX IF EXISTS idx_recommendation_security_status_weight;

CREATE INDEX IF NOT EXISTS idx_recommendation_security_status_weight
ON recommendation (
    cloud_account_id,
    status,
    (CASE WHEN severity = 'Critical' THEN 10
          WHEN severity = 'High'     THEN 8
          WHEN severity = 'Medium'   THEN 5
          WHEN severity = 'Low'      THEN 2
          WHEN severity = 'Info'     THEN 1
          ELSE 0 END) DESC,
    updated_at DESC
)
WHERE category = 'Security'
  AND rule_name = 'image_scan'
  AND account_object_id IS NOT NULL;
