DELETE FROM workflow_templates
WHERE is_system = true
  AND name IN (
    'Flush DNS Cache',
    'Run Network Connectivity Check',
    'Rotate Kubernetes Secret',
    'Restart RDS Instance',
    'Clear Redis Cache',
    'Restart Datadog Agent',
    'Silence Alert',
    'Scale Azure App Service'
  );
