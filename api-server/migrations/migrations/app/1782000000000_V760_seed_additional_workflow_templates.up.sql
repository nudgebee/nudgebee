-- Additional pre-built workflow templates for categories not covered by V708.
-- Each follows the approval → remediate → verify pattern.

-- ============================================================
-- Networking Templates
-- ============================================================

-- 1. Flush DNS Cache
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Flush DNS Cache',
  'Flush DNS cache on a Kubernetes node or CoreDNS. Useful for stale DNS resolution causing connectivity failures.',
  'networking',
  'dns',
  '{
    "version": "v1",
    "inputs": [
      {"id": "namespace", "type": "string", "description": "Kubernetes namespace for CoreDNS", "required": true, "default": "kube-system"},
      {"id": "cluster", "type": "string", "description": "Target cluster", "required": true},
      {"id": "account_id", "type": "string", "description": "Account ID", "required": true}
    ],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {"message": "Approve DNS cache flush in namespace {{ namespace }}?"}
      },
      {
        "id": "flush",
        "type": "kubernetes.rollout_restart",
        "params": {"namespace": "{{ namespace }}", "resource_type": "deployment", "resource_name": "coredns"},
        "depends_on": ["approve"]
      },
      {
        "id": "verify",
        "type": "kubernetes.check_pod_status",
        "params": {"namespace": "{{ namespace }}", "label_selector": "k8s-app=kube-dns"},
        "depends_on": ["flush"]
      }
    ]
  }',
  '[
    {"id": "namespace", "input_ref": "namespace", "display_name": "Namespace", "required": true, "type": "string", "placeholder": "kube-system"},
    {"id": "cluster", "input_ref": "cluster", "display_name": "Cluster", "required": true, "type": "cluster_selector"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["kubernetes", "prometheus", "alertmanager", "grafana_webhook"], "alert_names": ["CoreDNSDown", "KubeDNSDown", "DNSResolutionFailure"]}',
  true,
  'ACTIVE'
);

-- 2. Run Network Connectivity Check
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Run Network Connectivity Check',
  'Diagnose pod-to-pod or pod-to-service connectivity by running a network test pod. Useful for network policy or CNI issues.',
  'networking',
  'network_check',
  '{
    "version": "v1",
    "inputs": [
      {"id": "namespace", "type": "string", "description": "Namespace to test from", "required": true},
      {"id": "target_host", "type": "string", "description": "Target host or service to check connectivity to", "required": true},
      {"id": "target_port", "type": "string", "description": "Target port", "required": false, "default": "80"},
      {"id": "cluster", "type": "string", "description": "Target cluster", "required": true},
      {"id": "account_id", "type": "string", "description": "Account ID", "required": true}
    ],
    "tasks": [
      {
        "id": "check",
        "type": "kubernetes.run_command",
        "params": {
          "namespace": "{{ namespace }}",
          "image": "busybox:latest",
          "command": ["sh", "-c", "nc -zv {{ target_host }} {{ target_port }} && echo SUCCESS || echo FAILED"]
        }
      }
    ]
  }',
  '[
    {"id": "namespace", "input_ref": "namespace", "display_name": "Source Namespace", "required": true, "type": "string"},
    {"id": "target_host", "input_ref": "target_host", "display_name": "Target Host/Service", "required": true, "type": "string"},
    {"id": "target_port", "input_ref": "target_port", "display_name": "Target Port", "type": "string", "placeholder": "80"},
    {"id": "cluster", "input_ref": "cluster", "display_name": "Cluster", "required": true, "type": "cluster_selector"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["kubernetes", "prometheus", "alertmanager"], "alert_names": ["NetworkPolicyDrop", "PodConnectivityFailure"]}',
  true,
  'ACTIVE'
);

-- ============================================================
-- Security Templates
-- ============================================================

-- 3. Rotate Kubernetes Secret
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Rotate Kubernetes Secret',
  'Rotate a Kubernetes secret and restart dependent deployments. Useful for expired or compromised credentials.',
  'security',
  'lock_reset',
  '{
    "version": "v1",
    "inputs": [
      {"id": "namespace", "type": "string", "description": "Kubernetes namespace", "required": true},
      {"id": "secret_name", "type": "string", "description": "Name of the secret to rotate", "required": true},
      {"id": "deployment_name", "type": "string", "description": "Deployment to restart after rotation", "required": false},
      {"id": "cluster", "type": "string", "description": "Target cluster", "required": true},
      {"id": "account_id", "type": "string", "description": "Account ID", "required": true}
    ],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {"message": "Approve rotation of secret {{ secret_name }} in {{ namespace }}?"}
      },
      {
        "id": "rotate",
        "type": "kubernetes.patch_secret",
        "params": {"namespace": "{{ namespace }}", "secret_name": "{{ secret_name }}"},
        "depends_on": ["approve"]
      },
      {
        "id": "restart",
        "type": "kubernetes.rollout_restart",
        "params": {"namespace": "{{ namespace }}", "resource_type": "deployment", "resource_name": "{{ deployment_name }}"},
        "if": "{{ deployment_name }}",
        "depends_on": ["rotate"]
      },
      {
        "id": "verify",
        "type": "kubernetes.check_pod_status",
        "params": {"namespace": "{{ namespace }}", "label_selector": "app={{ deployment_name }}"},
        "if": "{{ deployment_name }}",
        "depends_on": ["restart"]
      }
    ]
  }',
  '[
    {"id": "namespace", "input_ref": "namespace", "display_name": "Namespace", "required": true, "type": "string"},
    {"id": "secret_name", "input_ref": "secret_name", "display_name": "Secret Name", "required": true, "type": "string"},
    {"id": "deployment_name", "input_ref": "deployment_name", "display_name": "Deployment to Restart", "type": "string", "help_text": "Leave empty to skip restart"},
    {"id": "cluster", "input_ref": "cluster", "display_name": "Cluster", "required": true, "type": "cluster_selector"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["kubernetes", "vault", "prometheus"], "alert_names": ["SecretExpired", "CertificateExpiring", "CredentialRotationRequired"]}',
  true,
  'ACTIVE'
);

-- ============================================================
-- Database Templates
-- ============================================================

-- 4. Restart RDS Instance
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Restart RDS Instance',
  'Reboot an AWS RDS instance. Useful for unresponsive databases, connection exhaustion, or parameter group changes.',
  'database',
  'restart_alt',
  '{
    "version": "v1",
    "inputs": [
      {"id": "db_instance_id", "type": "string", "description": "RDS instance identifier", "required": true},
      {"id": "region", "type": "string", "description": "AWS region", "required": true, "default": "us-east-1"},
      {"id": "account_id", "type": "string", "description": "Account ID", "required": true}
    ],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {"message": "Approve reboot of RDS instance {{ db_instance_id }} in {{ region }}?"}
      },
      {
        "id": "reboot",
        "type": "aws.rds_reboot",
        "params": {"db_instance_identifier": "{{ db_instance_id }}", "region": "{{ region }}"},
        "depends_on": ["approve"]
      },
      {
        "id": "verify",
        "type": "aws.rds_wait_available",
        "params": {"db_instance_identifier": "{{ db_instance_id }}", "region": "{{ region }}"},
        "depends_on": ["reboot"]
      }
    ]
  }',
  '[
    {"id": "db_instance_id", "input_ref": "db_instance_id", "display_name": "RDS Instance ID", "required": true, "type": "string"},
    {"id": "region", "input_ref": "region", "display_name": "AWS Region", "required": true, "type": "string", "placeholder": "us-east-1"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["cloudwatch", "prometheus", "datadog", "aws"], "alert_names": ["RDSHighCPU", "RDSConnectionExhaustion", "RDSStorageFull", "RDSReplicationLag"]}',
  true,
  'ACTIVE'
);

-- 5. Clear Redis Cache
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Clear Redis Cache',
  'Flush a Redis database to clear stale or corrupted cache. Useful for memory pressure or cache poisoning.',
  'database',
  'delete_sweep',
  '{
    "version": "v1",
    "inputs": [
      {"id": "namespace", "type": "string", "description": "Kubernetes namespace where Redis runs", "required": true},
      {"id": "redis_pod", "type": "string", "description": "Redis pod name or label selector", "required": true},
      {"id": "db_number", "type": "string", "description": "Redis DB number to flush (empty for all)", "required": false, "default": "0"},
      {"id": "cluster", "type": "string", "description": "Target cluster", "required": true},
      {"id": "account_id", "type": "string", "description": "Account ID", "required": true}
    ],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {"message": "Approve Redis FLUSHDB on db {{ db_number }} in pod {{ redis_pod }}?"}
      },
      {
        "id": "flush",
        "type": "kubernetes.exec",
        "params": {
          "namespace": "{{ namespace }}",
          "pod": "{{ redis_pod }}",
          "command": ["redis-cli", "-n", "{{ db_number }}", "FLUSHDB"]
        },
        "depends_on": ["approve"]
      },
      {
        "id": "verify",
        "type": "kubernetes.exec",
        "params": {
          "namespace": "{{ namespace }}",
          "pod": "{{ redis_pod }}",
          "command": ["redis-cli", "INFO", "memory"]
        },
        "depends_on": ["flush"]
      }
    ]
  }',
  '[
    {"id": "namespace", "input_ref": "namespace", "display_name": "Namespace", "required": true, "type": "string"},
    {"id": "redis_pod", "input_ref": "redis_pod", "display_name": "Redis Pod", "required": true, "type": "string"},
    {"id": "db_number", "input_ref": "db_number", "display_name": "DB Number", "type": "string", "placeholder": "0"},
    {"id": "cluster", "input_ref": "cluster", "display_name": "Cluster", "required": true, "type": "cluster_selector"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["prometheus", "datadog", "kubernetes"], "alert_names": ["RedisHighMemory", "RedisCacheMissRate", "RedisOOM"]}',
  true,
  'ACTIVE'
);

-- ============================================================
-- Monitoring Templates
-- ============================================================

-- 6. Restart Datadog Agent
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Restart Datadog Agent',
  'Restart the Datadog agent DaemonSet to recover missing metrics or logs from a node.',
  'monitoring',
  'monitoring',
  '{
    "version": "v1",
    "inputs": [
      {"id": "namespace", "type": "string", "description": "Namespace where Datadog agent runs", "required": true, "default": "datadog"},
      {"id": "daemonset_name", "type": "string", "description": "DaemonSet name", "required": true, "default": "datadog-agent"},
      {"id": "cluster", "type": "string", "description": "Target cluster", "required": true},
      {"id": "account_id", "type": "string", "description": "Account ID", "required": true}
    ],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {"message": "Approve restart of Datadog agent DaemonSet {{ daemonset_name }}?"}
      },
      {
        "id": "restart",
        "type": "kubernetes.rollout_restart",
        "params": {"namespace": "{{ namespace }}", "resource_type": "daemonset", "resource_name": "{{ daemonset_name }}"},
        "depends_on": ["approve"]
      },
      {
        "id": "verify",
        "type": "kubernetes.check_pod_status",
        "params": {"namespace": "{{ namespace }}", "label_selector": "app={{ daemonset_name }}"},
        "depends_on": ["restart"]
      }
    ]
  }',
  '[
    {"id": "namespace", "input_ref": "namespace", "display_name": "Namespace", "required": true, "type": "string", "placeholder": "datadog"},
    {"id": "daemonset_name", "input_ref": "daemonset_name", "display_name": "DaemonSet Name", "required": true, "type": "string", "placeholder": "datadog-agent"},
    {"id": "cluster", "input_ref": "cluster", "display_name": "Cluster", "required": true, "type": "cluster_selector"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["datadog", "kubernetes", "prometheus"], "alert_names": ["DatadogAgentDown", "MissingMetrics", "AgentUnreachable"]}',
  true,
  'ACTIVE'
);

-- ============================================================
-- General Templates
-- ============================================================

-- 7. Silence Alert
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Silence Alert',
  'Create a temporary silence for a known-noisy alert during maintenance windows or expected disruptions.',
  'general',
  'notifications_off',
  '{
    "version": "v1",
    "inputs": [
      {"id": "alert_name", "type": "string", "description": "Alert name to silence", "required": true},
      {"id": "duration_minutes", "type": "string", "description": "Silence duration in minutes", "required": true, "default": "60"},
      {"id": "reason", "type": "string", "description": "Reason for silencing", "required": true},
      {"id": "account_id", "type": "string", "description": "Account ID", "required": true}
    ],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {"message": "Approve silencing alert {{ alert_name }} for {{ duration_minutes }} minutes? Reason: {{ reason }}"}
      },
      {
        "id": "silence",
        "type": "alertmanager.create_silence",
        "params": {"alert_name": "{{ alert_name }}", "duration_minutes": "{{ duration_minutes }}", "comment": "{{ reason }}"},
        "depends_on": ["approve"]
      }
    ]
  }',
  '[
    {"id": "alert_name", "input_ref": "alert_name", "display_name": "Alert Name", "required": true, "type": "string"},
    {"id": "duration_minutes", "input_ref": "duration_minutes", "display_name": "Duration (minutes)", "required": true, "type": "string", "placeholder": "60"},
    {"id": "reason", "input_ref": "reason", "display_name": "Reason", "required": true, "type": "string"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["prometheus", "alertmanager", "grafana_webhook", "datadog", "pagerduty", "opsgenie"], "alert_names": []}',
  true,
  'ACTIVE'
);

-- 8. Scale Azure App Service
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Scale Azure App Service',
  'Scale an Azure App Service plan to handle increased load. Useful for high CPU or memory alerts.',
  'azure',
  'scale',
  '{
    "version": "v1",
    "inputs": [
      {"id": "resource_group", "type": "string", "description": "Azure resource group", "required": true},
      {"id": "app_service_plan", "type": "string", "description": "App Service plan name", "required": true},
      {"id": "target_sku", "type": "string", "description": "Target SKU tier (e.g. P1v3, P2v3)", "required": true},
      {"id": "account_id", "type": "string", "description": "Account ID", "required": true}
    ],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {"message": "Approve scaling App Service plan {{ app_service_plan }} to {{ target_sku }}?"}
      },
      {
        "id": "scale",
        "type": "azure.scale_app_service_plan",
        "params": {"resource_group": "{{ resource_group }}", "plan_name": "{{ app_service_plan }}", "sku": "{{ target_sku }}"},
        "depends_on": ["approve"]
      },
      {
        "id": "verify",
        "type": "azure.get_app_service_plan",
        "params": {"resource_group": "{{ resource_group }}", "plan_name": "{{ app_service_plan }}"},
        "depends_on": ["scale"]
      }
    ]
  }',
  '[
    {"id": "resource_group", "input_ref": "resource_group", "display_name": "Resource Group", "required": true, "type": "string"},
    {"id": "app_service_plan", "input_ref": "app_service_plan", "display_name": "App Service Plan", "required": true, "type": "string"},
    {"id": "target_sku", "input_ref": "target_sku", "display_name": "Target SKU", "required": true, "type": "string", "options": ["B1", "B2", "B3", "S1", "S2", "S3", "P1v3", "P2v3", "P3v3"]},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["azure_monitor", "datadog", "prometheus"], "alert_names": ["AppServiceHighCPU", "AppServiceHighMemory", "AppServicePlanScaleRequired"]}',
  true,
  'ACTIVE'
);
