
INSERT INTO agent_playbook_action (params, category, description, display_name, name) VALUES
('{}', 'All', 'Enriches alert with resource yaml', 'Get Resource YAML', 'get_resource_yaml'),
('{}', 'Node', 'Enrich the finding with analysis of the node''s CPU usage. Collect information about pods running on this node, their CPU request configuration, their actual cpu usage etc. Provides insightful information regarding node high CPU usage.', 'Node CPU Enricher', 'node_cpu_enricher'),
('{
  "show_pods": {
    "type": "bool",
    "default": true,
    "required": false,
    "description": "Show pods",
    "display_name": "Show Pods"
  },
  "show_containers": {
    "type": "bool",
    "default": false,
    "required": false,
    "description": "Show containers",
    "display_name": "Show Containers"
  }
}', 'Node', 'Provides relevant disk information for troubleshooting disk issues. Currently, the following information is provided by default, The total disk space used by pods, and the total disk space used by the node for other purposes, Disk usage of pods, sorted from highest to lowest', 'Node Disk Analyzer', 'node_disk_analyzer'),
('{}', 'Node', 'Enrich the finding with the status of the node.', 'Node Status Enricher', 'node_status_enricher'),
('{}', 'Pod', 'Report the pods that are in crash loop', 'Report Crash Loop', 'report_crash_loop'),
('{}', 'Pod', 'Enriches alert with pod information', 'Pod Enricher', 'pod_enricher'),
('{}', 'Pod', 'Investigate the issue with the pod', 'Pod Issue Investigator', 'pod_issue_investigator'),
('{
  "output_format": {
    "type": "string",
    "default": "table",
    "required": false,
    "description": "Output format",
    "display_name": "Output Format",
    "possible_values": [
      "table",
      "json"
    ]
  }
}', 'All', 'Enriches alert with related pods', 'Related Pods', 'related_pods'),
('{
  "previous": {
    "type": "bool",
    "default": false,
    "required": false,
    "description": "Fetch logs before the alert",
    "display_name": "Previous"
  },
  "tail_lines": {
    "type": "int",
    "default": 1000,
    "required": false,
    "description": "Number of lines to tail",
    "display_name": "Tail Lines"
  },
  "container_name": {
    "type": "string",
    "default": "",
    "required": false,
    "description": "Name of the container",
    "display_name": "Container Name"
  }
}', 'Pod', 'Enriches pod alert with logs', 'Logs Enricher', 'logs_enricher'),
('{
  "max_events": {
    "type": "int",
    "default": 8,
    "required": false,
    "description": "Maximum number of events to capture",
    "display_name": "Max Events"
  },
  "included_types": {
    "type": "list",
    "default": [
      "Normal",
      "Warning"
    ],
    "required": false,
    "description": "List of event types to include",
    "display_name": "Included Event Types",
    "possible_values": [
      "Normal",
      "Warning"
    ]
  }
}', 'Pod', 'Enriches alert with pod events', 'Pod Events Enricher', 'pod_events_enricher'),
('{
  "max_pods": {
    "type": "int",
    "default": 1,
    "required": false,
    "description": "Maximum number of pods to capture events",
    "display_name": "Max Pods"
  },
  "dependent_pod_mode": {
    "type": "bool",
    "default": false,
    "required": false,
    "description": "Enable dependent pod mode",
    "display_name": "When True, instead of fetching events for the deployment itself, fetch events for pods in the deployment"
  }
}', 'Deployment', 'Enriches alert with deployment events', 'Deployment Events Enricher', 'deployment_events_enricher'),
('{
  "max_events": {
    "type": "int",
    "default": 8,
    "required": false,
    "description": "Maximum number of events to capture",
    "display_name": "Max Events"
  },
  "included_types": {
    "type": "list",
    "default": [
      "Normal",
      "Warning"
    ],
    "required": false,
    "description": "Comma list of event types to include",
    "display_name": "Included Event Types",
    "possible_values": [
      "Normal",
      "Warning"
    ]
  }
}', 'Job', 'Enriches alert with job events', 'Job Events Enricher', 'job_events_enricher'),
('{}', 'Node', 'Enrich the finding with pods running on this node, along with the "Ready" status of each pod.', 'Node Running Pods Enricher', 'node_running_pods_enricher'),
('{}', 'Node', 'Enrich the finding with the allocatable resources of the node.', 'Node Allocatable Resources Enricher', 'node_allocatable_resources_enricher'),
('{
  "default_query_duration": {
    "type": "int",
    "default": 600,
    "required": false,
    "description": "Default query duration in seconds",
    "display_name": "Default Query Duration"
  }
}', 'Node', 'Enrich the finding with analysis of the CPU overcommitment on the node. Provides insightful information regarding node high CPU usage.', 'CPU Overcommited Enricher', 'cpu_overcommited_enricher'),
('{
  "default_query_duration": {
    "type": "int",
    "default": 600,
    "required": false,
    "description": "Default query duration in seconds",
    "display_name": "Default Query Duration"
  }
}', 'Cluster', 'Enrich the finding with the memory requests of the cluster.', 'Cluster Memory Requests Enricher', 'cluster_memory_requests_enricher'),
('{
  "command": {
    "type": "string",
    "default": "",
    "required": true,
    "description": "Kubectl command to execute",
    "display_name": "Command"
  }
}', 'All', 'Execute kubectl command', 'Kubectl Command Executor', 'kubectl_command_executor'),
('{
  "bash_command": {
    "type": "string",
    "default": "",
    "required": true,
    "description": "Bash command to run",
    "display_name": "Command"
  }
}', 'Pod', 'Run bash command in the pod', 'Pod Bash Enricher', 'pod_bash_enricher'),
('{
  "profile_type": {
    "type": "string",
    "default": "cpu",
    "required": true,
    "description": "Profile type",
    "display_name": "Profile Type",
    "possible_values": [
      "cpu",
      "memory"
    ]
  }
}', 'Pod', 'Profile the pod', 'Pod Profiler', 'pod_profiler'),
('{
  "image": {
    "type": "string",
    "default": "",
    "required": true,
    "description": "Custom image to run the script",
    "display_name": "Image"
  },
  "secret": {
    "type": "string",
    "default": "",
    "required": false,
    "description": "Secret to use for the custom image",
    "display_name": "Secret"
  },
  "command": {
    "type": "string",
    "default": [],
    "required": false,
    "description": "Command to run in the custom image",
    "display_name": "Command"
  }
}', 'All', 'Run script using custom image', 'Custom Image Run', 'pod_script_run_enricher') ON CONFLICT (name) 
DO UPDATE SET 
  params = EXCLUDED.params,
  category = EXCLUDED.category,
  description = EXCLUDED.description,
  display_name = EXCLUDED.display_name;

INSERT INTO agent_playbook_action (params, category, description, display_name, name) VALUES
('{
"max_pods": {
  "type": "int",
  "default": 1,
  "required": false,
  "description": "Maximum number of pods to capture events",
  "display_name": "Max Pods"
},
"dependent_pod_mode": {
  "type": "bool",
  "default": false,
  "required": false,
  "description": "Enable dependent pod mode",
  "display_name": "When True, instead of fetching events for the deployment itself, fetch events for pods in the deployment"
}
}', 'All', 'Enriches alert with additional nearby kubernetes events', 'Resource Events Enricher', 'resource_events_enricher'), 
('{
    "default_query_duration": {
      "type": "int",
      "default": 600,
      "required": false,
      "description": "Default query duration in seconds",
      "display_name": "Default Query Duration"
    }
  }', 'Node', 'Enrich the finding with analysis of the memory overcommitment on the node. Provides insightful information regarding node high memory usage.', 'Memory Overcommited Enricher', 'memory_overcommited_enricher'), 
  ('{
    "logs": {
      "type": "bool",
      "default": true,
      "required": false,
      "description": "Include logs",
      "display_name": "Logs"
    },
    "events": {
      "type": "bool",
      "default": true,
      "required": false,
      "description": "Include events",
      "display_name": "Events"
    }
  }', 'Job', 'Enriches alert with job pods', 'Job Pod Enricher', 'job_pod_enricher') ON CONFLICT (name) 
DO UPDATE SET 
  params = EXCLUDED.params,
  category = EXCLUDED.category,
  description = EXCLUDED.description,
  display_name = EXCLUDED.display_name;
