-- Remove the AI-assisted monitoring workflow template added in V761.
DELETE FROM workflow_templates
WHERE is_system = true
  AND tenant_id IS NULL
  AND account_id IS NULL
  AND name = 'Summarize Kubernetes Workload Errors to Chat';
