-- Add a net-new pre-built workflow template: namespace-wide cleanup of
-- Evicted / Failed / Succeeded pods. Complements the existing single-pod
-- "Force Delete Stuck Terminating Pod" and "Delete Failed Kubernetes Job"
-- templates by reclaiming clutter left behind across a whole namespace.

INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Clean Up Evicted and Failed Pods',
  'Delete Evicted, Failed, and Succeeded pods across a namespace to reclaim resources and reduce clutter. Useful after node-pressure evictions or completed batch runs leave terminated pods behind.',
  'kubernetes',
  'delete_sweep',
  '{
    "version": "v1",
    "inputs": [
      {"id": "namespace", "type": "string", "description": "Kubernetes namespace to clean up", "required": true},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "list_candidates",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "kubectl get pods -n {{ Inputs.namespace }} --field-selector status.phase=Failed -o wide && kubectl get pods -n {{ Inputs.namespace }} --field-selector status.phase=Succeeded -o wide",
          "account_id": "{{ Inputs.account_id }}"
        }
      },
      {
        "id": "approve",
        "type": "core.approval",
        "params": {
          "message": "Delete all Evicted/Failed and Succeeded pods in namespace **{{ Inputs.namespace }}**? Review the list above before approving."
        },
        "depends_on": ["list_candidates"]
      },
      {
        "id": "delete_failed",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "kubectl delete pods -n {{ Inputs.namespace }} --field-selector status.phase=Failed",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["approve"]
      },
      {
        "id": "delete_succeeded",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "kubectl delete pods -n {{ Inputs.namespace }} --field-selector status.phase=Succeeded",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["approve"]
      },
      {
        "id": "verify",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "if kubectl get pods -n {{ Inputs.namespace }} --field-selector status.phase=Failed -o name | grep -q . ; then echo ''Failed pods still present'' && exit 1; fi; if kubectl get pods -n {{ Inputs.namespace }} --field-selector status.phase=Succeeded -o name | grep -q . ; then echo ''Succeeded pods still present'' && exit 1; fi; echo ''All Failed/Succeeded pods cleaned up''",
          "account_id": "{{ Inputs.account_id }}"
        },
        "depends_on": ["delete_failed", "delete_succeeded"]
      }
    ],
    "timeout": "10m"
  }',
  '[
    {"id": "namespace", "input_ref": "namespace", "display_name": "Namespace", "required": true, "type": "string"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["prometheus", "kubernetes_api_server", "alertmanager"], "alert_names": ["KubePodEvicted", "KubeletTooManyPods", "KubePodCrashLooping"], "subject_types": ["pod", "namespace"]}',
  true,
  'ACTIVE'
);
