-- Remove the 6 new workflow templates added in V712
DELETE FROM workflow_templates WHERE is_system = true AND tenant_id IS NULL AND name IN (
  'Force Delete Stuck Terminating Pod',
  'Restart Deployment on High Error Logs',
  'Scale RabbitMQ Consumers',
  'Purge SQS Queue',
  'Investigate RDS Performance',
  'Restart Cloud SQL Instance'
);
