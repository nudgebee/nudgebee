-- Add subject_types tag to existing workflow templates for subject-type-based matching.
-- subject_type is system-derived from the resource (e.g. "pod", "deployment", "queue", "db")
-- and is stable across tenants, unlike alert_names which are user-defined.

-- K8s templates
UPDATE workflow_templates SET tags = tags || '{"subject_types": ["deployment"]}'::jsonb
WHERE name = 'Rollout Restart Deployment' AND is_system = true AND tenant_id IS NULL;

UPDATE workflow_templates SET tags = tags || '{"subject_types": ["deployment", "horizontalpodautoscaler"]}'::jsonb
WHERE name = 'Scale Kubernetes Replicas' AND is_system = true AND tenant_id IS NULL;

UPDATE workflow_templates SET tags = tags || '{"subject_types": ["node"]}'::jsonb
WHERE name = 'Cordon and Drain Kubernetes Node' AND is_system = true AND tenant_id IS NULL;

UPDATE workflow_templates SET tags = tags || '{"subject_types": ["job"]}'::jsonb
WHERE name = 'Delete Failed Kubernetes Job' AND is_system = true AND tenant_id IS NULL;

UPDATE workflow_templates SET tags = tags || '{"subject_types": ["pod", "persistentvolumeclaim"]}'::jsonb
WHERE name = 'Expand Persistent Volume Claim' AND is_system = true AND tenant_id IS NULL;

UPDATE workflow_templates SET tags = tags || '{"subject_types": ["pod", "deployment"]}'::jsonb
WHERE name = 'Patch Container Resource Limits' AND is_system = true AND tenant_id IS NULL;

-- AWS templates
UPDATE workflow_templates SET tags = tags || '{"subject_types": ["db", "db-instance"]}'::jsonb
WHERE name = 'Scale RDS Storage' AND is_system = true AND tenant_id IS NULL;

UPDATE workflow_templates SET tags = tags || '{"subject_types": ["compute-instance"]}'::jsonb
WHERE name = 'Restart EC2 Instance' AND is_system = true AND tenant_id IS NULL;

UPDATE workflow_templates SET tags = tags || '{"subject_types": ["service"]}'::jsonb
WHERE name = 'Force ECS Service Deployment' AND is_system = true AND tenant_id IS NULL;

UPDATE workflow_templates SET tags = tags || '{"subject_types": ["autoscaling-group"]}'::jsonb
WHERE name = 'Scale Auto Scaling Group' AND is_system = true AND tenant_id IS NULL;

-- Azure templates
UPDATE workflow_templates SET tags = tags || '{"subject_types": ["virtualmachines", "virtualmachine"]}'::jsonb
WHERE name = 'Restart Azure Virtual Machine' AND is_system = true AND tenant_id IS NULL;

UPDATE workflow_templates SET tags = tags || '{"subject_types": ["virtualmachines"]}'::jsonb
WHERE name = 'Expand Azure Managed Disk' AND is_system = true AND tenant_id IS NULL;

UPDATE workflow_templates SET tags = tags || '{"subject_types": ["sites"]}'::jsonb
WHERE name = 'Restart Azure App Service' AND is_system = true AND tenant_id IS NULL;

-- GCP templates
UPDATE workflow_templates SET tags = tags || '{"subject_types": ["gce_instance"]}'::jsonb
WHERE name = 'Restart GCP VM Instance' AND is_system = true AND tenant_id IS NULL;

UPDATE workflow_templates SET tags = tags || '{"subject_types": ["gce_disk"]}'::jsonb
WHERE name = 'Resize GCP Persistent Disk' AND is_system = true AND tenant_id IS NULL;

-- Universal template: empty subject_types so it matches via clause 3 (universal fallback)
UPDATE workflow_templates SET tags = tags || '{"subject_types": []}'::jsonb
WHERE name = 'Create Ticket from Event' AND is_system = true AND tenant_id IS NULL;
