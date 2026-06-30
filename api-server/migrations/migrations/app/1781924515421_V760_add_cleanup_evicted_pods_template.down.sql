-- Remove the workflow template added in V760.
DELETE FROM workflow_templates WHERE is_system = true AND tenant_id IS NULL AND name IN (
  'Clean Up Evicted and Failed Pods'
);
