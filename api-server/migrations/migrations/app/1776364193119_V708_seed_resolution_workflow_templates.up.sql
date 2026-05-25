-- Seed system workflow templates for event resolution suggestions.
-- Each template follows the pattern: approval → remediate → verify.
-- Tags contain event_sources and alert_names arrays for matching.

-- ============================================================
-- K8s Templates (6)
-- ============================================================

-- 1. Rollout Restart Deployment
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Rollout Restart Deployment',
  'Restart a Kubernetes deployment via rolling update. Useful for crash-looping pods, OOMKilled containers, or stale state.',
  'kubernetes',
  'restart_alt',
  '{
    "version": "v1",
    "inputs": [
      {"id": "namespace", "type": "string", "description": "Kubernetes namespace", "required": true},
      {"id": "deployment", "type": "string", "description": "Deployment name to restart", "required": true},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {
          "message": "Approve rolling restart of deployment **{{ Inputs.deployment }}** in namespace **{{ Inputs.namespace }}**?"
        }
      },
      {
        "id": "restart",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "kubectl rollout restart deployment/{{ Inputs.deployment }} -n {{ Inputs.namespace }}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["approve"]
      },
      {
        "id": "verify",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "kubectl rollout status deployment/{{ Inputs.deployment }} -n {{ Inputs.namespace }} --timeout=300s",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["restart"],
        "timeout": "6m"
      }
    ],
    "timeout": "15m"
  }',
  '[
    {"id": "namespace", "input_ref": "namespace", "display_name": "Namespace", "required": true, "type": "string"},
    {"id": "deployment", "input_ref": "deployment", "display_name": "Deployment Name", "required": true, "type": "string"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["prometheus", "alertmanager", "kubernetes"], "alert_names": ["KubePodCrashLooping", "KubePodNotReady", "KubeContainerOOMKilled", "KubeDeploymentReplicasMismatch"]}',
  true,
  'ACTIVE'
);

-- 2. Delete Failed Job
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Delete Failed Kubernetes Job',
  'Delete a failed Kubernetes job to allow re-creation or cleanup. Useful for stuck or errored batch jobs.',
  'kubernetes',
  'delete_sweep',
  '{
    "version": "v1",
    "inputs": [
      {"id": "namespace", "type": "string", "description": "Kubernetes namespace", "required": true},
      {"id": "job_name", "type": "string", "description": "Job name to delete", "required": true},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {
          "message": "Approve deletion of failed job **{{ Inputs.job_name }}** in namespace **{{ Inputs.namespace }}**?"
        }
      },
      {
        "id": "delete_job",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "kubectl delete job {{ Inputs.job_name }} -n {{ Inputs.namespace }}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["approve"]
      },
      {
        "id": "verify",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "kubectl get jobs -n {{ Inputs.namespace }} -o wide",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["delete_job"]
      }
    ],
    "timeout": "10m"
  }',
  '[
    {"id": "namespace", "input_ref": "namespace", "display_name": "Namespace", "required": true, "type": "string"},
    {"id": "job_name", "input_ref": "job_name", "display_name": "Job Name", "required": true, "type": "string"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["prometheus", "alertmanager", "kubernetes"], "alert_names": ["KubeJobFailed", "KubeJobNotCompleted", "KubeJobCompletion"]}',
  true,
  'ACTIVE'
);

-- 3. Scale Replicas
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Scale Kubernetes Replicas',
  'Scale a deployment to a specified replica count. Useful for handling high load or HPA issues.',
  'kubernetes',
  'straighten',
  '{
    "version": "v1",
    "inputs": [
      {"id": "namespace", "type": "string", "description": "Kubernetes namespace", "required": true},
      {"id": "deployment", "type": "string", "description": "Deployment name to scale", "required": true},
      {"id": "replicas", "type": "string", "description": "Target replica count", "required": true},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {
          "message": "Approve scaling **{{ Inputs.deployment }}** in **{{ Inputs.namespace }}** to **{{ Inputs.replicas }}** replicas?"
        }
      },
      {
        "id": "scale",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "kubectl scale deployment/{{ Inputs.deployment }} --replicas={{ Inputs.replicas }} -n {{ Inputs.namespace }}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["approve"]
      },
      {
        "id": "verify",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "kubectl get deployment {{ Inputs.deployment }} -n {{ Inputs.namespace }} -o wide",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["scale"]
      }
    ],
    "timeout": "10m"
  }',
  '[
    {"id": "namespace", "input_ref": "namespace", "display_name": "Namespace", "required": true, "type": "string"},
    {"id": "deployment", "input_ref": "deployment", "display_name": "Deployment Name", "required": true, "type": "string"},
    {"id": "replicas", "input_ref": "replicas", "display_name": "Replica Count", "required": true, "type": "string", "placeholder": "3"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["prometheus", "alertmanager", "kubernetes"], "alert_names": ["KubeHpaMaxedOut", "KubeHpaReplicasMismatch", "CPUThrottlingHigh", "KubeDeploymentReplicasMismatch"]}',
  true,
  'ACTIVE'
);

-- 4. Expand PVC
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Expand Persistent Volume Claim',
  'Increase the size of a PersistentVolumeClaim. Requires the storage class to support volume expansion.',
  'kubernetes',
  'sd_storage',
  '{
    "version": "v1",
    "inputs": [
      {"id": "namespace", "type": "string", "description": "Kubernetes namespace", "required": true},
      {"id": "pvc_name", "type": "string", "description": "PVC name to expand", "required": true},
      {"id": "new_size", "type": "string", "description": "New size (e.g. 50Gi)", "required": true},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {
          "message": "Approve expanding PVC **{{ Inputs.pvc_name }}** in **{{ Inputs.namespace }}** to **{{ Inputs.new_size }}**?"
        }
      },
      {
        "id": "expand",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "kubectl patch pvc {{ Inputs.pvc_name }} -n {{ Inputs.namespace }} -p {\"spec\":{\"resources\":{\"requests\":{\"storage\":\"{{ Inputs.new_size }}\"}}}}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["approve"]
      },
      {
        "id": "verify",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "kubectl get pvc {{ Inputs.pvc_name }} -n {{ Inputs.namespace }} -o wide",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["expand"]
      }
    ],
    "timeout": "10m"
  }',
  '[
    {"id": "namespace", "input_ref": "namespace", "display_name": "Namespace", "required": true, "type": "string"},
    {"id": "pvc_name", "input_ref": "pvc_name", "display_name": "PVC Name", "required": true, "type": "string"},
    {"id": "new_size", "input_ref": "new_size", "display_name": "New Size", "required": true, "type": "string", "placeholder": "50Gi"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["prometheus", "alertmanager", "kubernetes"], "alert_names": ["KubePersistentVolumeFillingUp", "KubePersistentVolumeInodesFillingUp"]}',
  true,
  'ACTIVE'
);

-- 5. Cordon and Drain Node
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Cordon and Drain Kubernetes Node',
  'Cordon a node to prevent new pods, then drain existing pods. Useful for problematic or unresponsive nodes.',
  'kubernetes',
  'block',
  '{
    "version": "v1",
    "inputs": [
      {"id": "node_name", "type": "string", "description": "Node name to cordon and drain", "required": true},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {
          "message": "Approve cordoning and draining node **{{ Inputs.node_name }}**? This will evict all pods from the node."
        }
      },
      {
        "id": "cordon",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "kubectl cordon {{ Inputs.node_name }}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["approve"]
      },
      {
        "id": "drain",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "kubectl drain {{ Inputs.node_name }} --ignore-daemonsets --delete-emptydir-data --timeout=300s",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["cordon"],
        "timeout": "6m"
      },
      {
        "id": "verify",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "kubectl get node {{ Inputs.node_name }} -o wide",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["drain"]
      }
    ],
    "timeout": "15m"
  }',
  '[
    {"id": "node_name", "input_ref": "node_name", "display_name": "Node Name", "required": true, "type": "string"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["prometheus", "alertmanager", "kubernetes"], "alert_names": ["KubeNodeNotReady", "KubeNodeUnreachable", "NodeFilesystemSpaceFillingUp", "NodeFilesystemAlmostOutOfSpace"]}',
  true,
  'ACTIVE'
);

-- 6. Patch Resource Limits
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Patch Container Resource Limits',
  'Update CPU and memory limits on a deployment. Useful for OOMKilled or CPU-throttled containers.',
  'kubernetes',
  'tune',
  '{
    "version": "v1",
    "inputs": [
      {"id": "namespace", "type": "string", "description": "Kubernetes namespace", "required": true},
      {"id": "deployment", "type": "string", "description": "Deployment name", "required": true},
      {"id": "container", "type": "string", "description": "Container name", "required": true},
      {"id": "cpu_limit", "type": "string", "description": "CPU limit (e.g. 500m, 1)", "required": true},
      {"id": "memory_limit", "type": "string", "description": "Memory limit (e.g. 512Mi, 1Gi)", "required": true},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {
          "message": "Approve updating resource limits for container **{{ Inputs.container }}** in **{{ Inputs.deployment }}** ({{ Inputs.namespace }})?\n\nCPU: {{ Inputs.cpu_limit }}\nMemory: {{ Inputs.memory_limit }}"
        }
      },
      {
        "id": "patch",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "kubectl set resources deployment/{{ Inputs.deployment }} -c {{ Inputs.container }} --limits=cpu={{ Inputs.cpu_limit }},memory={{ Inputs.memory_limit }} -n {{ Inputs.namespace }}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["approve"]
      },
      {
        "id": "verify",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "kubectl rollout status deployment/{{ Inputs.deployment }} -n {{ Inputs.namespace }} --timeout=300s",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["patch"],
        "timeout": "6m"
      }
    ],
    "timeout": "15m"
  }',
  '[
    {"id": "namespace", "input_ref": "namespace", "display_name": "Namespace", "required": true, "type": "string"},
    {"id": "deployment", "input_ref": "deployment", "display_name": "Deployment Name", "required": true, "type": "string"},
    {"id": "container", "input_ref": "container", "display_name": "Container Name", "required": true, "type": "string"},
    {"id": "cpu_limit", "input_ref": "cpu_limit", "display_name": "CPU Limit", "required": true, "type": "string", "placeholder": "500m"},
    {"id": "memory_limit", "input_ref": "memory_limit", "display_name": "Memory Limit", "required": true, "type": "string", "placeholder": "512Mi"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["prometheus", "alertmanager", "kubernetes"], "alert_names": ["KubeContainerOOMKilled", "CPUThrottlingHigh", "KubeMemoryOvercommit", "KubeCPUOvercommit"]}',
  true,
  'ACTIVE'
);

-- ============================================================
-- AWS Templates (4)
-- ============================================================

-- 7. Scale RDS Storage
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Scale RDS Storage',
  'Increase allocated storage for an RDS instance. Resolves low free storage alerts.',
  'aws',
  'database',
  '{
    "version": "v1",
    "inputs": [
      {"id": "db_instance_id", "type": "string", "description": "RDS DB instance identifier", "required": true},
      {"id": "new_storage_gb", "type": "string", "description": "New storage size in GB", "required": true},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {
          "message": "Approve scaling RDS instance **{{ Inputs.db_instance_id }}** storage to **{{ Inputs.new_storage_gb }} GB**?"
        }
      },
      {
        "id": "scale_storage",
        "type": "cloud.aws.cli",
        "params": {
          "command": "aws rds modify-db-instance --db-instance-identifier {{ Inputs.db_instance_id }} --allocated-storage {{ Inputs.new_storage_gb }} --apply-immediately",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["approve"]
      },
      {
        "id": "verify",
        "type": "cloud.aws.cli",
        "params": {
          "command": "aws rds describe-db-instances --db-instance-identifier {{ Inputs.db_instance_id }} --query DBInstances[0].{Status:DBInstanceStatus,Storage:AllocatedStorage}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["scale_storage"]
      }
    ],
    "timeout": "15m"
  }',
  '[
    {"id": "db_instance_id", "input_ref": "db_instance_id", "display_name": "DB Instance ID", "required": true, "type": "string"},
    {"id": "new_storage_gb", "input_ref": "new_storage_gb", "display_name": "New Storage (GB)", "required": true, "type": "string", "placeholder": "100"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["cloudwatch", "aws"], "alert_names": ["RDSFreeStorageSpace", "FreeStorageSpace", "DatabaseFreeStorageSpaceLow"]}',
  true,
  'ACTIVE'
);

-- 8. Restart EC2 Instance
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Restart EC2 Instance',
  'Reboot an EC2 instance. Useful for instance status check failures or unresponsive instances.',
  'aws',
  'power_settings_new',
  '{
    "version": "v1",
    "inputs": [
      {"id": "instance_id", "type": "string", "description": "EC2 instance ID (e.g. i-0abc123)", "required": true},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {
          "message": "Approve rebooting EC2 instance **{{ Inputs.instance_id }}**?"
        }
      },
      {
        "id": "reboot",
        "type": "cloud.aws.cli",
        "params": {
          "command": "aws ec2 reboot-instances --instance-ids {{ Inputs.instance_id }}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["approve"]
      },
      {
        "id": "verify",
        "type": "cloud.aws.cli",
        "params": {
          "command": "aws ec2 describe-instance-status --instance-ids {{ Inputs.instance_id }} --query InstanceStatuses[0].{State:InstanceState.Name,SystemStatus:SystemStatus.Status,InstanceStatus:InstanceStatus.Status}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["reboot"]
      }
    ],
    "timeout": "10m"
  }',
  '[
    {"id": "instance_id", "input_ref": "instance_id", "display_name": "Instance ID", "required": true, "type": "string", "placeholder": "i-0abc123def456"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["cloudwatch", "aws"], "alert_names": ["StatusCheckFailed", "StatusCheckFailed_Instance", "StatusCheckFailed_System"]}',
  true,
  'ACTIVE'
);

-- 9. Force ECS Service Deployment
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Force ECS Service Deployment',
  'Force a new deployment of an ECS service. Pulls fresh task definitions and replaces running tasks.',
  'aws',
  'replay',
  '{
    "version": "v1",
    "inputs": [
      {"id": "cluster", "type": "string", "description": "ECS cluster name", "required": true},
      {"id": "service_name", "type": "string", "description": "ECS service name", "required": true},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {
          "message": "Approve forcing new deployment for ECS service **{{ Inputs.service_name }}** in cluster **{{ Inputs.cluster }}**?"
        }
      },
      {
        "id": "deploy",
        "type": "cloud.aws.cli",
        "params": {
          "command": "aws ecs update-service --cluster {{ Inputs.cluster }} --service {{ Inputs.service_name }} --force-new-deployment",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["approve"]
      },
      {
        "id": "verify",
        "type": "cloud.aws.cli",
        "params": {
          "command": "aws ecs describe-services --cluster {{ Inputs.cluster }} --services {{ Inputs.service_name }} --query services[0].{Status:status,Running:runningCount,Desired:desiredCount,Deployments:deployments[*].status}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["deploy"]
      }
    ],
    "timeout": "10m"
  }',
  '[
    {"id": "cluster", "input_ref": "cluster", "display_name": "ECS Cluster", "required": true, "type": "string"},
    {"id": "service_name", "input_ref": "service_name", "display_name": "Service Name", "required": true, "type": "string"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["cloudwatch", "aws"], "alert_names": ["ECSServiceUnhealthy", "ECSTaskFailure", "ECSServiceCPUUtilization"]}',
  true,
  'ACTIVE'
);

-- 10. Scale Auto Scaling Group
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Scale Auto Scaling Group',
  'Set the desired capacity of an Auto Scaling Group. Useful for handling capacity-related alerts.',
  'aws',
  'unfold_more',
  '{
    "version": "v1",
    "inputs": [
      {"id": "asg_name", "type": "string", "description": "Auto Scaling Group name", "required": true},
      {"id": "desired_capacity", "type": "string", "description": "Desired instance count", "required": true},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {
          "message": "Approve scaling ASG **{{ Inputs.asg_name }}** to **{{ Inputs.desired_capacity }}** instances?"
        }
      },
      {
        "id": "scale",
        "type": "cloud.aws.cli",
        "params": {
          "command": "aws autoscaling set-desired-capacity --auto-scaling-group-name {{ Inputs.asg_name }} --desired-capacity {{ Inputs.desired_capacity }}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["approve"]
      },
      {
        "id": "verify",
        "type": "cloud.aws.cli",
        "params": {
          "command": "aws autoscaling describe-auto-scaling-groups --auto-scaling-group-names {{ Inputs.asg_name }} --query AutoScalingGroups[0].{Desired:DesiredCapacity,Min:MinSize,Max:MaxSize,InService:length(Instances[?LifecycleState==`InService`])}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["scale"]
      }
    ],
    "timeout": "10m"
  }',
  '[
    {"id": "asg_name", "input_ref": "asg_name", "display_name": "ASG Name", "required": true, "type": "string"},
    {"id": "desired_capacity", "input_ref": "desired_capacity", "display_name": "Desired Capacity", "required": true, "type": "string", "placeholder": "3"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["cloudwatch", "aws"], "alert_names": ["GroupInServiceInstances", "CPUUtilization", "ASGCapacityInsufficient"]}',
  true,
  'ACTIVE'
);

-- ============================================================
-- Azure Templates (3)
-- ============================================================

-- 11. Restart Azure VM
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Restart Azure Virtual Machine',
  'Restart an Azure VM. Useful for unresponsive VMs or after applying configuration changes.',
  'azure',
  'power_settings_new',
  '{
    "version": "v1",
    "inputs": [
      {"id": "resource_group", "type": "string", "description": "Azure resource group name", "required": true},
      {"id": "vm_name", "type": "string", "description": "VM name to restart", "required": true},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {
          "message": "Approve restarting Azure VM **{{ Inputs.vm_name }}** in resource group **{{ Inputs.resource_group }}**?"
        }
      },
      {
        "id": "restart",
        "type": "cloud.azure.cli",
        "params": {
          "command": "az vm restart --resource-group {{ Inputs.resource_group }} --name {{ Inputs.vm_name }}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["approve"]
      },
      {
        "id": "verify",
        "type": "cloud.azure.cli",
        "params": {
          "command": "az vm show --resource-group {{ Inputs.resource_group }} --name {{ Inputs.vm_name }} --show-details --query {powerState:powerState,provisioningState:provisioningState}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["restart"]
      }
    ],
    "timeout": "10m"
  }',
  '[
    {"id": "resource_group", "input_ref": "resource_group", "display_name": "Resource Group", "required": true, "type": "string"},
    {"id": "vm_name", "input_ref": "vm_name", "display_name": "VM Name", "required": true, "type": "string"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["azure_monitor", "azure"], "alert_names": ["VirtualMachineNotResponding", "VMAvailabilityIssue", "Percentage CPU"]}',
  true,
  'ACTIVE'
);

-- 12. Expand Azure Managed Disk
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Expand Azure Managed Disk',
  'Increase the size of an Azure managed disk. The VM must be deallocated or the disk must be unattached.',
  'azure',
  'sd_storage',
  '{
    "version": "v1",
    "inputs": [
      {"id": "resource_group", "type": "string", "description": "Azure resource group name", "required": true},
      {"id": "disk_name", "type": "string", "description": "Managed disk name", "required": true},
      {"id": "new_size_gb", "type": "string", "description": "New disk size in GB", "required": true},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {
          "message": "Approve expanding disk **{{ Inputs.disk_name }}** in **{{ Inputs.resource_group }}** to **{{ Inputs.new_size_gb }} GB**?\n\nNote: VM may need to be deallocated first."
        }
      },
      {
        "id": "expand",
        "type": "cloud.azure.cli",
        "params": {
          "command": "az disk update --resource-group {{ Inputs.resource_group }} --name {{ Inputs.disk_name }} --size-gb {{ Inputs.new_size_gb }}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["approve"]
      },
      {
        "id": "verify",
        "type": "cloud.azure.cli",
        "params": {
          "command": "az disk show --resource-group {{ Inputs.resource_group }} --name {{ Inputs.disk_name }} --query {size:diskSizeGb,state:diskState,provisioningState:provisioningState}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["expand"]
      }
    ],
    "timeout": "10m"
  }',
  '[
    {"id": "resource_group", "input_ref": "resource_group", "display_name": "Resource Group", "required": true, "type": "string"},
    {"id": "disk_name", "input_ref": "disk_name", "display_name": "Disk Name", "required": true, "type": "string"},
    {"id": "new_size_gb", "input_ref": "new_size_gb", "display_name": "New Size (GB)", "required": true, "type": "string", "placeholder": "128"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["azure_monitor", "azure"], "alert_names": ["DiskSpaceLow", "OSDiskFull", "Percentage Disk Used"]}',
  true,
  'ACTIVE'
);

-- 13. Restart Azure App Service
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Restart Azure App Service',
  'Restart an Azure App Service web app. Useful for HTTP 5xx errors or high response times.',
  'azure',
  'replay',
  '{
    "version": "v1",
    "inputs": [
      {"id": "resource_group", "type": "string", "description": "Azure resource group name", "required": true},
      {"id": "app_name", "type": "string", "description": "App Service name", "required": true},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {
          "message": "Approve restarting App Service **{{ Inputs.app_name }}** in resource group **{{ Inputs.resource_group }}**?"
        }
      },
      {
        "id": "restart",
        "type": "cloud.azure.cli",
        "params": {
          "command": "az webapp restart --resource-group {{ Inputs.resource_group }} --name {{ Inputs.app_name }}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["approve"]
      },
      {
        "id": "verify",
        "type": "cloud.azure.cli",
        "params": {
          "command": "az webapp show --resource-group {{ Inputs.resource_group }} --name {{ Inputs.app_name }} --query {state:state,availability:availabilityState}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["restart"]
      }
    ],
    "timeout": "10m"
  }',
  '[
    {"id": "resource_group", "input_ref": "resource_group", "display_name": "Resource Group", "required": true, "type": "string"},
    {"id": "app_name", "input_ref": "app_name", "display_name": "App Service Name", "required": true, "type": "string"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["azure_monitor", "azure"], "alert_names": ["AppServiceUnhealthy", "Http5xx", "ResponseTime", "HealthCheckStatus"]}',
  true,
  'ACTIVE'
);

-- ============================================================
-- GCP Templates (2)
-- ============================================================

-- 14. Restart GCP VM Instance
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Restart GCP VM Instance',
  'Reset a GCP Compute Engine VM instance. Useful for unresponsive VMs or uptime check failures.',
  'gcp',
  'power_settings_new',
  '{
    "version": "v1",
    "inputs": [
      {"id": "project", "type": "string", "description": "GCP project ID", "required": true},
      {"id": "zone", "type": "string", "description": "GCP zone (e.g. us-central1-a)", "required": true},
      {"id": "instance_name", "type": "string", "description": "VM instance name", "required": true},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {
          "message": "Approve resetting GCP VM **{{ Inputs.instance_name }}** in zone **{{ Inputs.zone }}** (project: {{ Inputs.project }})?"
        }
      },
      {
        "id": "reset",
        "type": "cloud.gcp.cli",
        "params": {
          "command": "gcloud compute instances reset {{ Inputs.instance_name }} --zone={{ Inputs.zone }} --project={{ Inputs.project }}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["approve"]
      },
      {
        "id": "verify",
        "type": "cloud.gcp.cli",
        "params": {
          "command": "gcloud compute instances describe {{ Inputs.instance_name }} --zone={{ Inputs.zone }} --project={{ Inputs.project }} --format=json(status,name)",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["reset"]
      }
    ],
    "timeout": "10m"
  }',
  '[
    {"id": "project", "input_ref": "project", "display_name": "GCP Project", "required": true, "type": "string"},
    {"id": "zone", "input_ref": "zone", "display_name": "Zone", "required": true, "type": "string", "placeholder": "us-central1-a"},
    {"id": "instance_name", "input_ref": "instance_name", "display_name": "Instance Name", "required": true, "type": "string"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["gcp_monitoring", "gcp"], "alert_names": ["VMInstanceUnresponsive", "UptimeCheckFailed", "InstanceCPUUtilization"]}',
  true,
  'ACTIVE'
);

-- 15. Resize GCP Persistent Disk
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Resize GCP Persistent Disk',
  'Increase the size of a GCP persistent disk. Does not require stopping the VM.',
  'gcp',
  'sd_storage',
  '{
    "version": "v1",
    "inputs": [
      {"id": "project", "type": "string", "description": "GCP project ID", "required": true},
      {"id": "zone", "type": "string", "description": "GCP zone (e.g. us-central1-a)", "required": true},
      {"id": "disk_name", "type": "string", "description": "Persistent disk name", "required": true},
      {"id": "new_size_gb", "type": "string", "description": "New disk size in GB", "required": true},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {
          "message": "Approve resizing disk **{{ Inputs.disk_name }}** in zone **{{ Inputs.zone }}** to **{{ Inputs.new_size_gb }} GB**?"
        }
      },
      {
        "id": "resize",
        "type": "cloud.gcp.cli",
        "params": {
          "command": "gcloud compute disks resize {{ Inputs.disk_name }} --zone={{ Inputs.zone }} --project={{ Inputs.project }} --size={{ Inputs.new_size_gb }}GB --quiet",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["approve"]
      },
      {
        "id": "verify",
        "type": "cloud.gcp.cli",
        "params": {
          "command": "gcloud compute disks describe {{ Inputs.disk_name }} --zone={{ Inputs.zone }} --project={{ Inputs.project }} --format=json(sizeGb,status,name)",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["resize"]
      }
    ],
    "timeout": "10m"
  }',
  '[
    {"id": "project", "input_ref": "project", "display_name": "GCP Project", "required": true, "type": "string"},
    {"id": "zone", "input_ref": "zone", "display_name": "Zone", "required": true, "type": "string", "placeholder": "us-central1-a"},
    {"id": "disk_name", "input_ref": "disk_name", "display_name": "Disk Name", "required": true, "type": "string"},
    {"id": "new_size_gb", "input_ref": "new_size_gb", "display_name": "New Size (GB)", "required": true, "type": "string", "placeholder": "200"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["gcp_monitoring", "gcp"], "alert_names": ["DiskUsageHigh", "PersistentDiskFull"]}',
  true,
  'ACTIVE'
);

-- ============================================================
-- Generic Template (1)
-- ============================================================

-- 16. Create Ticket from Event
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Create Ticket from Event',
  'Create a ticket in your configured ticketing system from an event. Works with Jira, GitHub Issues, ServiceNow, and other integrations.',
  'general',
  'confirmation_number',
  '{
    "version": "v1",
    "inputs": [
      {"id": "title", "type": "string", "description": "Ticket title", "required": true},
      {"id": "description", "type": "string", "description": "Ticket description", "required": true},
      {"id": "integration_id", "type": "string", "description": "Ticket integration ID", "required": true},
      {"id": "project_key", "type": "string", "description": "Project key (e.g. PROJ for Jira, owner/repo for GitHub)", "required": true},
      {"id": "ticket_type", "type": "string", "description": "Ticket type (Task, Bug, Incident)", "default": "Incident"},
      {"id": "severity", "type": "string", "description": "Priority/severity level", "default": "High"},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {
          "message": "Approve creating a **{{ Inputs.ticket_type }}** ticket in **{{ Inputs.project_key }}**?\n\nTitle: {{ Inputs.title }}"
        }
      },
      {
        "id": "create_ticket",
        "type": "tickets.create",
        "params": {
          "integration_id": "{{ Inputs.integration_id }}",
          "project_key": "{{ Inputs.project_key }}",
          "title": "{{ Inputs.title }}",
          "description": "{{ Inputs.description }}",
          "ticket_type": "{{ Inputs.ticket_type }}",
          "severity": "{{ Inputs.severity }}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["approve"]
      }
    ],
    "output": {
      "ticket_id": "{{ Tasks.create_ticket.output.ticket_id }}",
      "ticket_url": "{{ Tasks.create_ticket.output.url }}"
    },
    "timeout": "10m"
  }',
  '[
    {"id": "title", "input_ref": "title", "display_name": "Ticket Title", "required": true, "type": "string"},
    {"id": "description", "input_ref": "description", "display_name": "Description", "required": true, "type": "string"},
    {"id": "integration_id", "input_ref": "integration_id", "display_name": "Ticket Integration", "required": true, "type": "string"},
    {"id": "project_key", "input_ref": "project_key", "display_name": "Project Key", "required": true, "type": "string", "placeholder": "PROJ"},
    {"id": "ticket_type", "input_ref": "ticket_type", "display_name": "Ticket Type", "type": "string", "options": ["Incident", "Bug", "Task"]},
    {"id": "severity", "input_ref": "severity", "display_name": "Severity", "type": "string", "options": ["Critical", "High", "Medium", "Low"]},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["prometheus", "alertmanager", "cloudwatch", "azure_monitor", "gcp_monitoring", "datadog", "pagerduty", "opsgenie", "kubernetes", "aws", "azure", "gcp"], "alert_names": []}',
  true,
  'ACTIVE'
);
