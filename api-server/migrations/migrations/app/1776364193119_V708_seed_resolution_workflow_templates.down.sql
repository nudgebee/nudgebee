-- Remove seeded system workflow templates
DELETE FROM workflow_templates
WHERE is_system = true
  AND tenant_id IS NULL
  AND account_id IS NULL
  AND name IN (
    'Rollout Restart Deployment',
    'Delete Failed Kubernetes Job',
    'Scale Kubernetes Replicas',
    'Expand Persistent Volume Claim',
    'Cordon and Drain Kubernetes Node',
    'Patch Container Resource Limits',
    'Scale RDS Storage',
    'Restart EC2 Instance',
    'Force ECS Service Deployment',
    'Scale Auto Scaling Group',
    'Restart Azure Virtual Machine',
    'Expand Azure Managed Disk',
    'Restart Azure App Service',
    'Restart GCP VM Instance',
    'Resize GCP Persistent Disk',
    'Create Ticket from Event'
  );
