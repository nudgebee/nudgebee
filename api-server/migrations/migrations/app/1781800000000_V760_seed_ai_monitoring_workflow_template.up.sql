-- Add an AI-assisted monitoring template that summarizes Kubernetes workload logs
-- and posts the findings to the configured chat provider.

INSERT INTO workflow_templates (
  tenant_id, account_id, name, description, category, icon, definition,
  template_variables, tags, is_system, status
) VALUES (
  NULL, NULL,
  'Summarize Kubernetes Workload Errors to Chat',
  'Fetch recent logs for a Kubernetes workload, generate an AI summary of likely issues, and send the findings to Slack, Microsoft Teams, or Google Chat.',
  'monitoring',
  'analytics',
  '{
    "version": "v1",
    "inputs": [
      {"id": "namespace", "type": "string", "description": "Kubernetes namespace", "required": true},
      {"id": "workload_ref", "type": "string", "description": "Workload reference such as deployment/api or statefulset/redis", "required": true},
      {"id": "lookback", "type": "string", "description": "How far back to read logs", "default": "15m"},
      {"id": "tail_lines", "type": "string", "description": "Maximum number of log lines to read", "default": "200"},
      {"id": "provider", "type": "string", "description": "Chat provider", "default": "slack"},
      {"id": "channel", "type": "string", "description": "Destination channel or room", "required": true},
      {"id": "team_id", "type": "string", "description": "Microsoft Teams team ID when required"},
      {"id": "account_id", "type": "string", "description": "Cloud account ID", "required": true}
    ],
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "fetch_logs",
        "type": "cloud.k8s.cli",
        "params": {
          "command": "kubectl logs {{ Inputs.workload_ref }} -n {{ Inputs.namespace }} --since={{ Inputs.lookback }} --tail={{ Inputs.tail_lines }} --all-containers",
          "account_id": "{{ Inputs.account_id }}"
        }
      },
      {
        "id": "summarize_logs",
        "type": "llm.summary",
        "params": {
          "message": "Summarize the Kubernetes workload logs below. Focus on the most likely issue, any repeating error patterns, user impact, and the most useful next actions. Keep the answer concise and operational.\\n\\nNamespace: {{ Inputs.namespace }}\\nWorkload: {{ Inputs.workload_ref }}\\nLookback: {{ Inputs.lookback }}\\n\\nLogs:\\n{{ Tasks.fetch_logs.output.data }}"
        },
        "depends_on": ["fetch_logs"]
      },
      {
        "id": "post_summary",
        "type": "notifications.im",
        "params": {
          "provider": "{{ Inputs.provider }}",
          "team_id": "{{ Inputs.team_id }}",
          "channel": "{{ Inputs.channel }}",
          "message": "*AI log summary for {{ Inputs.workload_ref }}*\\nNamespace: {{ Inputs.namespace }}\\nWindow: {{ Inputs.lookback }}\\n\\n{{ Tasks.summarize_logs.output.data }}"
        },
        "depends_on": ["summarize_logs"]
      }
    ],
    "output": {
      "summary": "{{ Tasks.summarize_logs.output.data }}",
      "message_id": "{{ Tasks.post_summary.output.message_id }}"
    },
    "timeout": "10m"
  }',
  '[
    {"id": "namespace", "input_ref": "namespace", "display_name": "Namespace", "required": true, "type": "string"},
    {"id": "workload_ref", "input_ref": "workload_ref", "display_name": "Workload Reference", "required": true, "type": "string", "placeholder": "deployment/api"},
    {"id": "lookback", "input_ref": "lookback", "display_name": "Log Lookback", "type": "string", "placeholder": "15m"},
    {"id": "tail_lines", "input_ref": "tail_lines", "display_name": "Tail Lines", "type": "string", "placeholder": "200"},
    {"id": "provider", "input_ref": "provider", "display_name": "Chat Provider", "type": "string", "options": ["slack", "ms_teams", "google_chat"]},
    {"id": "channel", "input_ref": "channel", "display_name": "Channel or Room", "required": true, "type": "string", "placeholder": "#alerts"},
    {"id": "team_id", "input_ref": "team_id", "display_name": "Team ID", "type": "string", "placeholder": "Required for Microsoft Teams"},
    {"id": "account_id", "input_ref": "account_id", "display_name": "Account", "required": true, "type": "account_selector"}
  ]',
  '{"event_sources": ["prometheus", "alertmanager", "kubernetes_api_server"], "alert_names": ["HighErrorCriticalLogs", "ApplicationAPIFailures", "KubePodCrashLooping", "KubePodNotReady"], "subject_types": ["deployment", "pod", "statefulset", "daemonset"]}',
  true,
  'ACTIVE'
);
