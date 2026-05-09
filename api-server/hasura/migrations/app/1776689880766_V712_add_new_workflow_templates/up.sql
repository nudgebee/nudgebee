-- Add 6 new workflow templates that leverage subject_type matching.
-- These cover common event patterns not addressed by existing templates.

-- ============================================================
-- K8s Templates (3)
-- ============================================================

-- 1. Force Delete Stuck Terminating Pod
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Force Delete Stuck Terminating Pod',
  'Force-delete a pod stuck in Terminating state by skipping graceful shutdown. Useful when a pod cannot be drained or its finalizers are blocked.',
  'kubernetes',
  'delete_forever',
  '{
    "version": "v1",
    "inputs": [
      {"id": "namespace", "type": "string", "description": "Kubernetes namespace", "required": true},
      {"id": "pod_name", "type": "string", "description": "Name of the stuck pod", "required": true},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "approve",
        "type": "core.approval",
        "params": {
          "message": "Force delete pod **{{ Inputs.pod_name }}** in namespace **{{ Inputs.namespace }}**? This will skip graceful shutdown."
        }
      },
      {
        "id": "force_delete",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "kubectl delete pod {{ Inputs.pod_name }} -n {{ Inputs.namespace }} --grace-period=0 --force",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["approve"]
      },
      {
        "id": "verify",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "if kubectl get pods -n {{ Inputs.namespace }} | grep -qw {{ Inputs.pod_name }}; then echo ''Error: Pod still exists''; exit 1; else echo ''Pod deleted successfully''; fi",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["force_delete"]
      }
    ],
    "timeout": "5m"
  }',
  '[
    {"id": "namespace", "input_ref": "namespace", "display_name": "Namespace", "required": true, "type": "string"},
    {"id": "pod_name", "input_ref": "pod_name", "display_name": "Pod Name", "required": true, "type": "string"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["prometheus", "kubernetes_api_server", "alertmanager"], "alert_names": ["KubePodStuckTerminating"], "subject_types": ["pod"]}',
  true,
  'ACTIVE'
);

-- 2. Restart Deployment on High Error Logs
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Restart Deployment on High Error Logs',
  'Collect recent logs and perform a rolling restart of a deployment experiencing high error rates. Useful for transient application failures.',
  'kubernetes',
  'restart_alt',
  '{
    "version": "v1",
    "inputs": [
      {"id": "namespace", "type": "string", "description": "Kubernetes namespace", "required": true},
      {"id": "deployment", "type": "string", "description": "Deployment name", "required": true},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "collect_logs",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "kubectl logs deployment/{{ Inputs.deployment }} -n {{ Inputs.namespace }} --tail=50 --all-containers",
          "account_id": "{{ Inputs.account_id }}"
        }
      },
      {
        "id": "approve",
        "type": "core.approval",
        "params": {
          "message": "Recent logs collected. Approve rolling restart of **{{ Inputs.deployment }}** in **{{ Inputs.namespace }}**?"
        },
        "depends_on": ["collect_logs"]
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
        "depends_on": ["restart"]
      }
    ],
    "timeout": "10m"
  }',
  '[
    {"id": "namespace", "input_ref": "namespace", "display_name": "Namespace", "required": true, "type": "string"},
    {"id": "deployment", "input_ref": "deployment", "display_name": "Deployment Name", "required": true, "type": "string"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["prometheus", "kubernetes_api_server", "alertmanager"], "alert_names": ["HighErrorCriticalLogs", "ApplicationAPIFailures"], "subject_types": ["deployment", "pod", "daemonset", "statefulset"]}',
  true,
  'ACTIVE'
);

-- 3. Scale RabbitMQ Consumers
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Scale RabbitMQ Consumers',
  'Scale a consumer deployment to handle RabbitMQ queue backlog. Useful when queue depth grows due to insufficient consumer capacity.',
  'kubernetes',
  'tune',
  '{
    "version": "v1",
    "inputs": [
      {"id": "namespace", "type": "string", "description": "Kubernetes namespace of the consumer deployment", "required": true},
      {"id": "deployment", "type": "string", "description": "Consumer deployment name", "required": true},
      {"id": "target_replicas", "type": "string", "description": "Target replica count", "required": true},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "current_state",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "kubectl get deployment {{ Inputs.deployment }} -n {{ Inputs.namespace }} -o jsonpath=''{.spec.replicas} replicas, {.status.readyReplicas} ready''",
          "account_id": "{{ Inputs.account_id }}"
        }
      },
      {
        "id": "approve",
        "type": "core.approval",
        "params": {
          "message": "Scale **{{ Inputs.deployment }}** in **{{ Inputs.namespace }}** to **{{ Inputs.target_replicas }}** replicas?"
        },
        "depends_on": ["current_state"]
      },
      {
        "id": "scale",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "kubectl scale deployment {{ Inputs.deployment }} -n {{ Inputs.namespace }} --replicas={{ Inputs.target_replicas }}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["approve"]
      },
      {
        "id": "verify",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "kubectl rollout status deployment/{{ Inputs.deployment }} -n {{ Inputs.namespace }} --timeout=120s",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["scale"]
      }
    ],
    "timeout": "5m"
  }',
  '[
    {"id": "namespace", "input_ref": "namespace", "display_name": "Namespace", "required": true, "type": "string"},
    {"id": "deployment", "input_ref": "deployment", "display_name": "Consumer Deployment", "required": true, "type": "string"},
    {"id": "target_replicas", "input_ref": "target_replicas", "display_name": "Target Replicas", "required": true, "type": "string"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["prometheus", "kubernetes_api_server", "alertmanager"], "alert_names": ["RabbitmqTooManyReadyMessages", "RabbitmqNoQueueConsumer"], "subject_types": ["deployment", "pod"]}',
  true,
  'ACTIVE'
);

-- ============================================================
-- AWS Templates (2)
-- ============================================================

-- 4. Purge SQS Queue
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Purge SQS Queue',
  'Purge all messages from an SQS queue. Useful when a queue is backed up with stale or poison messages that block processing.',
  'aws',
  'delete_sweep',
  '{
    "version": "v1",
    "inputs": [
      {"id": "queue_url", "type": "string", "description": "Full SQS queue URL", "required": true},
      {"id": "region", "type": "string", "description": "AWS region (e.g. us-east-1)", "required": true},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "check_depth",
        "type": "cloud.aws.cli",
        "params": {
          "command": "aws sqs get-queue-attributes --queue-url {{ Inputs.queue_url }} --attribute-names ApproximateNumberOfMessages ApproximateNumberOfMessagesNotVisible --region {{ Inputs.region }}",
          "account_id": "{{ Inputs.account_id }}"
        }
      },
      {
        "id": "approve",
        "type": "core.approval",
        "params": {
          "message": "Purge all messages from SQS queue?\n\n**Queue:** {{ Inputs.queue_url }}\n**Region:** {{ Inputs.region }}\n\nThis will permanently delete all messages in the queue."
        },
        "depends_on": ["check_depth"]
      },
      {
        "id": "purge",
        "type": "cloud.aws.cli",
        "params": {
          "command": "aws sqs purge-queue --queue-url {{ Inputs.queue_url }} --region {{ Inputs.region }}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["approve"]
      },
      {
        "id": "verify",
        "type": "cloud.aws.cli",
        "params": {
          "command": "aws sqs get-queue-attributes --queue-url {{ Inputs.queue_url }} --attribute-names ApproximateNumberOfMessages --region {{ Inputs.region }}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["purge"]
      }
    ],
    "timeout": "5m"
  }',
  '[
    {"id": "queue_url", "input_ref": "queue_url", "display_name": "SQS Queue URL", "required": true, "type": "string", "placeholder": "https://sqs.us-east-1.amazonaws.com/123456789012/my-queue"},
    {"id": "region", "input_ref": "region", "display_name": "AWS Region", "required": true, "type": "string", "placeholder": "us-east-1"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["AWS_CloudWatch_Alarm", "AWS_EventBridge"], "alert_names": [], "subject_types": ["queue"]}',
  true,
  'ACTIVE'
);

-- 5. Investigate RDS Performance
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Investigate RDS Performance',
  'Gather RDS instance details, recent events, CPU utilization, and connection metrics. Provides a quick diagnostic snapshot for database performance issues.',
  'aws',
  'analytics',
  '{
    "version": "v1",
    "inputs": [
      {"id": "db_instance_id", "type": "string", "description": "RDS DB instance identifier", "required": true},
      {"id": "region", "type": "string", "description": "AWS region (e.g. us-east-1)", "required": true},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "describe_instance",
        "type": "cloud.aws.cli",
        "params": {
          "command": "aws rds describe-db-instances --db-instance-identifier {{ Inputs.db_instance_id }} --region {{ Inputs.region }} --query ''DBInstances[0].{Status:DBInstanceStatus,Class:DBInstanceClass,Engine:Engine,Storage:AllocatedStorage,MultiAZ:MultiAZ}''",
          "account_id": "{{ Inputs.account_id }}"
        }
      },
      {
        "id": "check_events",
        "type": "cloud.aws.cli",
        "params": {
          "command": "aws rds describe-events --source-identifier {{ Inputs.db_instance_id }} --source-type db-instance --duration 60 --region {{ Inputs.region }}",
          "account_id": "{{ Inputs.account_id }}"
        }
      },
      {
        "id": "check_cpu",
        "type": "cloud.aws.cli",
        "params": {
          "command": "aws cloudwatch get-metric-statistics --namespace AWS/RDS --metric-name CPUUtilization --dimensions Name=DBInstanceIdentifier,Value={{ Inputs.db_instance_id }} --start-time $(date -u -d ''1 hour ago'' +%Y-%m-%dT%H:%M:%S 2>/dev/null || date -u -v-1H +%Y-%m-%dT%H:%M:%S) --end-time $(date -u +%Y-%m-%dT%H:%M:%S) --period 300 --statistics Average Maximum --region {{ Inputs.region }}",
          "account_id": "{{ Inputs.account_id }}"
        }
      },
      {
        "id": "check_connections",
        "type": "cloud.aws.cli",
        "params": {
          "command": "aws cloudwatch get-metric-statistics --namespace AWS/RDS --metric-name DatabaseConnections --dimensions Name=DBInstanceIdentifier,Value={{ Inputs.db_instance_id }} --start-time $(date -u -d ''1 hour ago'' +%Y-%m-%dT%H:%M:%S 2>/dev/null || date -u -v-1H +%Y-%m-%dT%H:%M:%S) --end-time $(date -u +%Y-%m-%dT%H:%M:%S) --period 300 --statistics Average Maximum --region {{ Inputs.region }}",
          "account_id": "{{ Inputs.account_id }}"
        }
      }
    ],
    "timeout": "5m"
  }',
  '[
    {"id": "db_instance_id", "input_ref": "db_instance_id", "display_name": "DB Instance ID", "required": true, "type": "string"},
    {"id": "region", "input_ref": "region", "display_name": "AWS Region", "required": true, "type": "string", "placeholder": "us-east-1"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["AWS_CloudWatch_Alarm", "AWS_EventBridge"], "alert_names": [], "subject_types": ["db", "db-instance"]}',
  true,
  'ACTIVE'
);

-- ============================================================
-- GCP Template (1)
-- ============================================================

-- 6. Restart Cloud SQL Instance
INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Restart Cloud SQL Instance',
  'Restart a GCP Cloud SQL instance. Useful for applying configuration changes, recovering from connection exhaustion, or clearing transient issues.',
  'gcp',
  'restart_alt',
  '{
    "version": "v1",
    "inputs": [
      {"id": "instance_name", "type": "string", "description": "Cloud SQL instance name", "required": true},
      {"id": "project", "type": "string", "description": "GCP project ID", "required": true},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "check_status",
        "type": "cloud.gcp.cli",
        "params": {
          "command": "gcloud sql instances describe {{ Inputs.instance_name }} --project={{ Inputs.project }} --format=''table(state,settings.tier,databaseVersion,settings.dataDiskSizeGb)''",
          "account_id": "{{ Inputs.account_id }}"
        }
      },
      {
        "id": "approve",
        "type": "core.approval",
        "params": {
          "message": "Approve restart of Cloud SQL instance **{{ Inputs.instance_name }}** in project **{{ Inputs.project }}**?\n\nThis will cause brief downtime."
        },
        "depends_on": ["check_status"]
      },
      {
        "id": "restart",
        "type": "cloud.gcp.cli",
        "params": {
          "command": "gcloud sql instances restart {{ Inputs.instance_name }} --project={{ Inputs.project }}",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["approve"]
      },
      {
        "id": "wait",
        "type": "core.wait",
        "params": {
          "duration": "30s"
        },
        "depends_on": ["restart"]
      },
      {
        "id": "verify",
        "type": "cloud.gcp.cli",
        "params": {
          "command": "gcloud sql instances describe {{ Inputs.instance_name }} --project={{ Inputs.project }} --format=''value(state)''",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["wait"]
      }
    ],
    "timeout": "10m"
  }',
  '[
    {"id": "instance_name", "input_ref": "instance_name", "display_name": "Instance Name", "required": true, "type": "string"},
    {"id": "project", "input_ref": "project", "display_name": "GCP Project ID", "required": true, "type": "string"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["GCP_Metric_Alert", "gcp_monitoring_webhook"], "alert_names": [], "subject_types": ["cloudsql_database"]}',
  true,
  'ACTIVE'
);
