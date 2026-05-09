package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/tools/core"
	"strings"

	"github.com/google/uuid"
)

func init() {
	core.RegisterNBToolFactory(ToolWorkflowList, func(accountId string) (core.NBTool, error) {
		return WorkflowListTool{}, nil
	})
	core.RegisterNBToolFactory(ToolWorkflowGet, func(accountId string) (core.NBTool, error) {
		return WorkflowGetTool{}, nil
	})
	core.RegisterNBToolFactory(ToolWorkflowTrigger, func(accountId string) (core.NBTool, error) {
		return WorkflowTriggerTool{}, nil
	})
	core.RegisterNBToolFactory(ToolWorkflowCreate, func(accountId string) (core.NBTool, error) {
		return WorkflowCreateTool{}, nil
	})
	core.RegisterNBToolFactory(ToolWorkflowValidate, func(accountId string) (core.NBTool, error) {
		return WorkflowValidateTool{}, nil
	})
	core.RegisterNBToolFactory(ToolWorkflowTaskList, func(accountId string) (core.NBTool, error) {
		return TaskListTool{}, nil
	})
	core.RegisterNBToolFactory(ToolWorkflowExamples, func(accountId string) (core.NBTool, error) {
		return WorkflowExamplesTool{}, nil
	})
	core.RegisterNBToolFactory(ToolWorkflowTaskSchema, func(accountId string) (core.NBTool, error) {
		return TaskSchemaLookupTool{}, nil
	})
	core.RegisterNBToolFactory(ToolWorkflowUpdate, func(accountId string) (core.NBTool, error) {
		return WorkflowUpdateTool{}, nil
	})
	core.RegisterNBToolFactory(ToolWorkflowExecutions, func(accountId string) (core.NBTool, error) {
		return WorkflowExecutionsTool{}, nil
	})
	core.RegisterNBToolFactory(ToolWorkflowExecutionGet, func(accountId string) (core.NBTool, error) {
		return WorkflowExecutionGetTool{}, nil
	})
	core.RegisterNBToolFactory(ToolWorkflowExecutionRetrigger, func(accountId string) (core.NBTool, error) {
		return WorkflowExecutionRetriggerTool{}, nil
	})
	core.RegisterNBToolFactory(ToolWorkflowState, func(accountId string) (core.NBTool, error) {
		return WorkflowStateTool{}, nil
	})
	core.RegisterNBToolFactory(ToolWorkflowConfigList, func(accountId string) (core.NBTool, error) {
		return WorkflowConfigListTool{}, nil
	})
	core.RegisterNBToolFactory(ToolWorkflowConfigGet, func(accountId string) (core.NBTool, error) {
		return WorkflowConfigGetTool{}, nil
	})
	core.RegisterNBToolFactory(ToolWorkflowConfigSave, func(accountId string) (core.NBTool, error) {
		return WorkflowConfigSaveTool{}, nil
	})
}

const (
	ToolWorkflowList               = "workflow_list"
	ToolWorkflowGet                = "workflow_get"
	ToolWorkflowTrigger            = "workflow_trigger"
	ToolWorkflowCreate             = "workflow_create"
	ToolWorkflowValidate           = "workflow_validate"
	ToolWorkflowTaskList           = "workflow_task_list"
	ToolWorkflowExamples           = "workflow_examples"
	ToolWorkflowTaskSchema         = "workflow_task_schema"
	ToolWorkflowUpdate             = "workflow_update"
	ToolWorkflowExecutions         = "workflow_executions"
	ToolWorkflowExecutionGet       = "workflow_execution_get"
	ToolWorkflowExecutionRetrigger = "workflow_execution_retrigger"
	ToolWorkflowState              = "workflow_state"
	ToolWorkflowConfigList         = "workflow_config_list"
	ToolWorkflowConfigGet          = "workflow_config_get"
	ToolWorkflowConfigSave         = "workflow_config_save"
)

func getRunbookServerURL(path string) string {
	url := strings.TrimSpace(config.Config.WorkflowServerEndpoint)
	if url != "" && !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		slog.Warn("workflow: endpoint missing scheme, defaulting to http", "endpoint", url)
		url = "http://" + url
	}
	if !strings.HasSuffix(url, "/") {
		url += "/"
	}
	// remove leading slash from path if present to avoid double slash
	path = strings.TrimPrefix(path, "/")
	return url + path
}

func DoRunbookRequest(method, path string, body any, accountId, tenantId, userId string) ([]byte, error) {
	url := getRunbookServerURL(path)

	var reqBody io.Reader
	if body != nil {
		jsonBytes, err := common.MarshalJson(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonBytes)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	// Assuming authentication via token or account ID headers is handled similarly to other services
	req.Header.Set("X-Account-ID", accountId)
	req.Header.Set("X-Tenant-ID", tenantId)
	if userId != "" && userId != uuid.Nil.String() {
		req.Header.Set("X-User-ID", userId)
	}

	client := common.HttpClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("runbook server returned error (status %d): %s", resp.StatusCode, string(respBytes))
	}

	return respBytes, nil
}

// --- Workflow List Tool ---

type WorkflowListTool struct{}

func (t WorkflowListTool) Name() string             { return ToolWorkflowList }
func (t WorkflowListTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (t WorkflowListTool) Description() string {
	return "Lists existing automations with optional filtering."
}
func (t WorkflowListTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"limit": {Type: core.ToolSchemaTypeString, Description: "Number of automations to return (default 20)"},
			"name":  {Type: core.ToolSchemaTypeString, Description: "Filter by automation name (partial match)"},
		},
	}
}
func (t WorkflowListTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	queryParams := []string{}
	if val, ok := input.Arguments["limit"]; ok {
		queryParams = append(queryParams, fmt.Sprintf("limit=%v", val))
	}
	if val, ok := input.Arguments["name"]; ok {
		queryParams = append(queryParams, fmt.Sprintf("name=%v", val))
	}

	path := "workflows"
	if len(queryParams) > 0 {
		path += "?" + strings.Join(queryParams, "&")
	}

	resp, err := DoRunbookRequest("GET", path, nil, ctx.AccountId, ctx.Ctx.GetSecurityContext().GetTenantId(), ctx.Ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return core.NBToolResponse{}, err
	}
	return core.NBToolResponse{Data: string(resp), Type: core.NBToolResponseTypeJson}, nil
}

// --- Workflow Get Tool ---

type WorkflowGetTool struct{}

func (t WorkflowGetTool) Name() string             { return ToolWorkflowGet }
func (t WorkflowGetTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (t WorkflowGetTool) Description() string {
	return "Gets the definition and details of a specific automation."
}
func (t WorkflowGetTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"id": {Type: core.ToolSchemaTypeString, Description: "The ID of the automation to retrieve"},
		},
		Required: []string{"id"},
	}
}
func (t WorkflowGetTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	id, ok := input.Arguments["id"].(string)
	if !ok || id == "" {
		return core.NBToolResponse{}, errors.New("id is required")
	}
	resp, err := DoRunbookRequest("GET", fmt.Sprintf("workflows/%s", id), nil, ctx.AccountId, ctx.Ctx.GetSecurityContext().GetTenantId(), ctx.Ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return core.NBToolResponse{}, err
	}

	// For large workflows (>8KB), return a compact summary to fit within the ReAct scratchpad budget
	const compactThreshold = 8192
	if len(resp) > compactThreshold {
		compact := compactWorkflowResponse(resp)
		if compact != "" {
			return core.NBToolResponse{Data: compact, Type: core.NBToolResponseTypeJson}, nil
		}
	}

	return core.NBToolResponse{Data: string(resp), Type: core.NBToolResponseTypeJson}, nil
}

// compactWorkflowResponse returns a structured summary of a large workflow,
// preserving task IDs, types, dependencies, and conditions while omitting large param values.
func compactWorkflowResponse(resp []byte) string {
	var workflow map[string]interface{}
	if err := json.Unmarshal(resp, &workflow); err != nil {
		return ""
	}

	summary := map[string]interface{}{
		"id":     workflow["id"],
		"name":   workflow["name"],
		"status": workflow["status"],
	}

	definition, ok := workflow["definition"].(map[string]interface{})
	if !ok {
		return ""
	}

	compactDef := map[string]interface{}{
		"version":  definition["version"],
		"triggers": definition["triggers"],
	}

	if inputs, ok := definition["inputs"]; ok {
		compactDef["inputs"] = inputs
	}

	tasks, ok := definition["tasks"].([]interface{})
	if !ok {
		return ""
	}

	compactTasks := make([]map[string]interface{}, 0, len(tasks))
	for _, t := range tasks {
		task, ok := t.(map[string]interface{})
		if !ok {
			continue
		}

		ct := map[string]interface{}{
			"id":   task["id"],
			"type": task["type"],
		}
		if deps, ok := task["depends_on"]; ok {
			ct["depends_on"] = deps
		}
		if cond, ok := task["if"]; ok {
			ct["if"] = cond
		}

		// Include param keys but truncate large values
		if params, ok := task["params"].(map[string]interface{}); ok {
			compactParams := make(map[string]interface{}, len(params))
			for k, v := range params {
				if strVal, ok := v.(string); ok && len(strVal) > 200 {
					compactParams[k] = strVal[:200] + "...(truncated)"
				} else {
					compactParams[k] = v
				}
			}
			ct["params"] = compactParams
		}

		compactTasks = append(compactTasks, ct)
	}

	compactDef["tasks"] = compactTasks
	summary["definition"] = compactDef

	result, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return ""
	}
	return string(result)
}

// --- Workflow Trigger Tool ---

type WorkflowTriggerTool struct{}

func (t WorkflowTriggerTool) Name() string             { return ToolWorkflowTrigger }
func (t WorkflowTriggerTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (t WorkflowTriggerTool) Description() string      { return "Triggers an automation execution." }
func (t WorkflowTriggerTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"id":     {Type: core.ToolSchemaTypeString, Description: "The ID of the automation to trigger"},
			"inputs": {Type: core.ToolSchemaTypeObject, Description: "Input parameters for the automation execution"},
		},
		Required: []string{"id"},
	}
}
func (t WorkflowTriggerTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	id, ok := input.Arguments["id"].(string)
	if !ok || id == "" {
		return core.NBToolResponse{}, errors.New("id is required")
	}

	inputs := input.Arguments["inputs"]
	if inputs == nil {
		inputs = map[string]any{}
	}

	resp, err := DoRunbookRequest("POST", fmt.Sprintf("workflows/%s/trigger", id), inputs, ctx.AccountId, ctx.Ctx.GetSecurityContext().GetTenantId(), ctx.Ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return core.NBToolResponse{}, err
	}
	return core.NBToolResponse{Data: string(resp), Type: core.NBToolResponseTypeJson}, nil
}

// --- Workflow Create Tool ---

type WorkflowCreateTool struct{}

func (t WorkflowCreateTool) Name() string             { return ToolWorkflowCreate }
func (t WorkflowCreateTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (t WorkflowCreateTool) Description() string      { return "Creates a new automation." }
func (t WorkflowCreateTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"definition": {Type: core.ToolSchemaTypeObject, Description: "The automation definition (JSON/YAML structure)"},
		},
		Required: []string{"definition"},
	}
}
func (t WorkflowCreateTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	// The input args might contain the whole definition mixed in or nested.
	// Expecting the agent to pass the workflow object directly or under a key.
	// Based on schema, it should be under "definition".

	definition, ok := input.Arguments["definition"]
	if !ok {
		// Fallback: try to use the whole args if it looks like a workflow
		definition = input.Arguments
	}

	// Ensure definition is an object (LLM may pass it as a JSON string)
	definition, err := EnsureDefinitionObject(definition)
	if err != nil {
		return core.NBToolResponse{}, fmt.Errorf("invalid definition: %w", err)
	}

	resp, err := DoRunbookRequest("POST", "workflows", definition, ctx.AccountId, ctx.Ctx.GetSecurityContext().GetTenantId(), ctx.Ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return core.NBToolResponse{}, err
	}
	return core.NBToolResponse{Data: string(resp), Type: core.NBToolResponseTypeJson}, nil
}

// --- Workflow Validate Tool ---

type WorkflowValidateTool struct{}

func (t WorkflowValidateTool) Name() string             { return ToolWorkflowValidate }
func (t WorkflowValidateTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (t WorkflowValidateTool) Description() string {
	return "Validates an automation definition without creating it."
}
func (t WorkflowValidateTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"definition": {Type: core.ToolSchemaTypeObject, Description: "The automation definition to validate"},
		},
		Required: []string{"definition"},
	}
}
func (t WorkflowValidateTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	definition, ok := input.Arguments["definition"]
	if !ok {
		definition = input.Arguments
	}

	resp, err := DoRunbookRequest("POST", "workflows/validate", definition, ctx.AccountId, ctx.Ctx.GetSecurityContext().GetTenantId(), ctx.Ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return core.NBToolResponse{}, err
	}
	return core.NBToolResponse{Data: string(resp), Type: core.NBToolResponseTypeJson}, nil
}

// --- Task List Tool ---

type TaskListTool struct{}

func (t TaskListTool) Name() string             { return ToolWorkflowTaskList }
func (t TaskListTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (t TaskListTool) Description() string {
	return "Lists available tasks and their input/output schemas."
}
func (t TaskListTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type:       core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{},
	}
}
func (t TaskListTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	resp, err := DoRunbookRequest("GET", "tasks", nil, ctx.AccountId, ctx.Ctx.GetSecurityContext().GetTenantId(), ctx.Ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return core.NBToolResponse{}, err
	}
	return core.NBToolResponse{Data: string(resp), Type: core.NBToolResponseTypeJson}, nil
}

// --- Workflow Examples Tool ---

type WorkflowExamplesTool struct{}

func (t WorkflowExamplesTool) Name() string             { return ToolWorkflowExamples }
func (t WorkflowExamplesTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (t WorkflowExamplesTool) Description() string {
	return "Returns real production automation examples filtered by task types. Shows correct Jinja2 syntax, conditionals, state management patterns."
}
func (t WorkflowExamplesTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"task_types": {
				Type:        core.ToolSchemaTypeArray,
				Description: "Task types to filter examples by (e.g., ['k8s.cli', 'notifications.im'])",
			},
		},
		Required: []string{},
	}
}
func (t WorkflowExamplesTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	// Production workflow examples with correct Jinja2 syntax
	examples := map[string]string{
		"simple": `{
  "name": "cluster-health-check",
  "definition": {
    "version": "v1",
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "review-health",
        "type": "llm.investigate",
        "params": {
          "message": "Investigate cluster health in nudgebee namespace"
        }
      },
      {
        "id": "send-notification",
        "depends_on": ["review-health"],
        "type": "notifications.im",
        "params": {
          "message": "{{ Tasks['review-health'].output.data }}",
          "channel": "{{ Configs.slack_testnotification_channel }}",
          "provider": "slack"
        }
      }
    ]
  }
}`,
		"conditional": `{
  "name": "logs-monitoring",
  "definition": {
    "version": "v1",
    "inputs": [
      {"id": "namespace", "type": "string", "default": "nudgebee", "description": "monitoring namespace"}
    ],
    "triggers": [
      {"type": "schedule", "params": {"cron": "0 * * * *", "overlap_policy": "Skip"}}
    ],
    "tasks": [
      {
        "id": "list-logs",
        "type": "observability.log_groups",
        "params": {
          "start_time": "{{ now() | time_add('-1h') }}",
          "end_time": "{{ now() }}",
          "namespace": "{{ Inputs.namespace }}"
        }
      },
      {
        "id": "notify-errors",
        "depends_on": ["list-logs"],
        "if": "{{ Tasks['list-logs'].output.groups | length > 0 }}",
        "type": "notifications.im",
        "params": {
          "provider": "slack",
          "channel": "{{ Configs.slack_channel }}",
          "message": "Found {{ Tasks['list-logs'].output.groups | length }} error logs"
        }
      }
    ]
  }
}`,
		"state_management": `{
  "name": "threaded-notifications",
  "definition": {
    "version": "v1",
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "create-thread",
        "type": "notifications.im",
        "if": "{{ State['log_thread_' ~ (now() | date_format('2006-01-02'))] == null }}",
        "params": {
          "provider": "slack",
          "channel": "{{ Configs.slack_channel }}",
          "message": "Log Monitoring for {{ now() | date_format('2006-01-02') }}"
        },
        "set_state": {
          "log_thread_{{ now() | date_format('2006-01-02') }}": "{{ Self.output.message_id }}"
        }
      },
      {
        "id": "send-update",
        "depends_on": ["create-thread"],
        "if": "{{ Tasks['create-thread'].status == 'COMPLETED' or Tasks['create-thread'].status == 'SKIPPED' }}",
        "type": "notifications.im",
        "params": {
          "provider": "slack",
          "channel": "{{ Configs.slack_channel }}",
          "message_thread_id": "{{ State['log_thread_' ~ (now() | date_format('2006-01-02'))] }}",
          "message": "Status update"
        }
      }
    ]
  }
}`,
		"data_transform": `{
  "name": "aws-alarm-processing",
  "definition": {
    "version": "v1",
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "get-alarms",
        "type": "cloud.aws.cli",
        "params": {
          "account_id": "{{ Configs.aws_account_id }}",
          "command": "aws cloudwatch describe-alarms --region us-east-1 --output json"
        }
      },
      {
        "id": "extract-data",
        "type": "data.transform",
        "depends_on": ["get-alarms"],
        "params": {
          "input": "{{ Tasks['get-alarms'].output.data }}",
          "inputType": "json",
          "expression": "{\"metric\": MetricAlarms[0].MetricName, \"namespace\": MetricAlarms[0].Namespace}"
        }
      }
    ]
  }
}`,
		"foreach_loop": `{
  "name": "multi-namespace-health",
  "definition": {
    "version": "v1",
    "triggers": [{"type": "schedule", "params": {"cron": "0 */6 * * *", "overlap_policy": "Skip"}}],
    "inputs": [
      {"id": "namespaces", "type": "json", "default": ["nudgebee", "monitoring", "default"], "description": "Namespaces to check"}
    ],
    "tasks": [
      {
        "id": "check-namespaces",
        "type": "core.foreach",
        "params": {
          "items": "{{ Inputs.namespaces }}",
          "concurrency": 3,
          "tasks": [
            {
              "id": "check-pods",
              "type": "k8s.cli",
              "params": {"command": "get pods -n {{ item }} --field-selector=status.phase!=Running -o json"}
            },
            {
              "id": "notify-if-issues",
              "depends_on": ["check-pods"],
              "if": "{{ Tasks['check-pods'].output.data | to_json | length > 10 }}",
              "type": "notifications.im",
              "params": {
                "provider": "slack",
                "channel": "{{ Configs.slack_warroom_channel }}",
                "message": "Issues in namespace {{ item }}: {{ Tasks['check-pods'].output.data }}"
              }
            }
          ]
        }
      }
    ]
  }
}`,
		"scripting_http": `{
  "name": "api-health-check",
  "definition": {
    "version": "v1",
    "triggers": [{"type": "schedule", "params": {"cron": "*/5 * * * *", "overlap_policy": "Skip"}}],
    "tasks": [
      {
        "id": "check-endpoints",
        "type": "scripting.run_script",
        "params": {
          "language": "python",
          "parser_type": "json",
          "script": "import json\nimport urllib.request\ntry:\n    endpoints = ['https://api.example.com/health', 'https://api.example.com/status']\n    results = []\n    for url in endpoints:\n        try:\n            req = urllib.request.urlopen(url, timeout=10)\n            results.append({'url': url, 'status': req.status, 'healthy': True})\n        except Exception as e:\n            results.append({'url': url, 'error': str(e), 'healthy': False})\n    print(json.dumps({'endpoints': results, 'unhealthy_count': sum(1 for r in results if not r['healthy'])}))\nexcept Exception as e:\n    print(json.dumps({'error': str(e), 'endpoints': [], 'unhealthy_count': -1}))\n"
        },
        "failure_policy": {"retry": {"maximum_attempts": 2, "initial_interval": "5s"}}
      },
      {
        "id": "alert-if-unhealthy",
        "depends_on": ["check-endpoints"],
        "if": "{{ (Tasks['check-endpoints'].output.data | default({})).unhealthy_count | default(0) > 0 }}",
        "type": "notifications.im",
        "params": {
          "provider": "slack",
          "channel": "{{ Configs.slack_warroom_channel }}",
          "message": "API Health Alert: {{ Tasks['check-endpoints'].output.data.unhealthy_count }} unhealthy endpoints detected"
        }
      }
    ]
  }
}`,
		"matrix_bulk_ops": `{
  "name": "stale-tickets-report",
  "definition": {
    "version": "v1",
    "triggers": [{"type": "schedule", "params": {"cron": "0 9 * * MON", "overlap_policy": "Skip"}}],
    "tasks": [
      {
        "id": "fetch-stale-tickets",
        "type": "scripting.run_script",
        "params": {
          "language": "python",
          "parser_type": "json",
          "script": "import json, urllib.request\ntry:\n    # Fetch open issues older than 14 days\n    url = 'https://api.github.com/repos/org/repo/issues?state=open&per_page=50'\n    req = urllib.request.Request(url, headers={'Accept': 'application/json'})\n    resp = urllib.request.urlopen(req)\n    issues = json.loads(resp.read())\n    stale = [{'number': i['number'], 'title': i['title']} for i in issues if i.get('pull_request') is None]\n    print(json.dumps({'issues': stale[:20], 'count': len(stale)}))\nexcept Exception as e:\n    print(json.dumps({'error': str(e), 'issues': [], 'count': 0}))"
        }
      },
      {
        "id": "label-stale-tickets",
        "type": "scm.github.cli",
        "depends_on": ["fetch-stale-tickets"],
        "if": "{{ Tasks['fetch-stale-tickets'].output.data.count > 0 }}",
        "matrix": {
          "issue": "{{ Tasks['fetch-stale-tickets'].output.data.issues }}"
        },
        "failure_policy": {"action": "continue"},
        "params": {
          "integration_id": "{{ Configs.github_integration_id }}",
          "command": "gh issue edit {{ Matrix.issue.number | int }} --repo org/repo --add-label stale"
        }
      },
      {
        "id": "notify-summary",
        "depends_on": ["label-stale-tickets"],
        "type": "notifications.im",
        "params": {
          "provider": "slack",
          "channel": "{{ Configs.slack_channel }}",
          "message": "Labeled {{ Tasks['fetch-stale-tickets'].output.data.count }} stale tickets. See https://github.com/org/repo/labels/stale"
        }
      }
    ]
  }
}`,
		"python_data_processing": `{
  "name": "hn-top-stories",
  "definition": {
    "version": "v1",
    "triggers": [{"type": "manual"}],
    "tasks": [
      {
        "id": "fetch-top-stories",
        "type": "integrations.http",
        "params": {
          "url": "https://hacker-news.firebaseio.com/v0/topstories.json",
          "method": "GET"
        }
      },
      {
        "id": "slice-top-3",
        "type": "scripting.run_script",
        "depends_on": ["fetch-top-stories"],
        "params": {
          "language": "python",
          "parser_type": "json",
          "script": "import json\ntry:\n    ids = json.loads('''{{ Tasks['fetch-top-stories'].output.body | to_json }}''')\n    print(json.dumps(ids[:3]))\nexcept Exception as e:\n    print(json.dumps({'error': str(e)}))"
        }
      },
      {
        "id": "fetch-stories",
        "type": "core.foreach",
        "depends_on": ["slice-top-3"],
        "params": {
          "items": "{{ Tasks['slice-top-3'].output.data }}",
          "item": "story_id",
          "concurrency": 3,
          "tasks": [
            {
              "id": "get-story",
              "type": "integrations.http",
              "params": {
                "url": "https://hacker-news.firebaseio.com/v0/item/{{ story_id }}.json",
                "method": "GET"
              }
            }
          ]
        }
      },
      {
        "id": "summarize",
        "type": "scripting.run_script",
        "depends_on": ["fetch-stories"],
        "params": {
          "language": "python",
          "parser_type": "json",
          "script": "import json\ntry:\n    results = json.loads('''{{ Tasks['fetch-stories'].output.results | to_json }}''')\n    stories = []\n    for r in results:\n        body = r.get('output', {}).get('body', {})\n        if isinstance(body, str):\n            body = json.loads(body)\n        stories.append({'title': body.get('title', 'N/A'), 'url': body.get('url', ''), 'score': body.get('score', 0)})\n    print(json.dumps({'stories': stories}))\nexcept Exception as e:\n    print(json.dumps({'error': str(e), 'stories': []}))"
        }
      },
      {
        "id": "print-summary",
        "type": "core.print",
        "depends_on": ["summarize"],
        "params": {
          "message": "Top 3 HN Stories:\n{{ Tasks['summarize'].output.data | to_json }}"
        }
      }
    ]
  }
}`,
		"github_cli": `{
  "name": "trigger-deploy-workflow",
  "definition": {
    "version": "v1",
    "triggers": [{"type": "manual"}],
    "inputs": [
      {"id": "repo", "type": "string", "default": "org/service", "description": "GitHub repo"},
      {"id": "workflow_file", "type": "string", "default": "deploy.yaml", "description": "Workflow file name"}
    ],
    "tasks": [
      {
        "id": "trigger-workflow",
        "type": "scm.github.cli",
        "params": {
          "integration_id": "{{ Configs.github_integration_id }}",
          "command": "gh workflow run {{ Inputs.workflow_file }} --repo {{ Inputs.repo }}"
        }
      },
      {
        "id": "notify-triggered",
        "depends_on": ["trigger-workflow"],
        "type": "notifications.im",
        "params": {
          "provider": "slack",
          "channel": "{{ Configs.slack_channel }}",
          "message": "Triggered {{ Inputs.workflow_file }} on {{ Inputs.repo }}"
        }
      }
    ]
  }
}`}

	// Filter examples based on task_types if provided
	taskTypes, ok := input.Arguments["task_types"].([]interface{})
	var result strings.Builder
	result.WriteString("# Production Automation Examples\n\n")

	if ok && len(taskTypes) > 0 {
		// Return examples that contain the requested task types
		fmt.Fprintf(&result, "Examples filtered by task types: %v\n\n", taskTypes)
		for name, example := range examples {
			fmt.Fprintf(&result, "## Example: %s\n```json\n%s\n```\n\n", name, example)
		}
	} else {
		// Return all examples
		result.WriteString("## Simple Automation (llm.investigate + notifications.im)\n")
		result.WriteString("```json\n" + examples["simple"] + "\n```\n\n")
		result.WriteString("## Conditional Execution (if field)\n")
		result.WriteString("```json\n" + examples["conditional"] + "\n```\n\n")
		result.WriteString("## State Management (set_state, message threading)\n")
		result.WriteString("```json\n" + examples["state_management"] + "\n```\n\n")
	}

	result.WriteString("\n## Key Patterns:\n")
	result.WriteString("- Jinja2 syntax: {{ Tasks['task_id'].output.data }}\n")
	result.WriteString("- Conditionals: \"if\": \"{{ condition }}\"\n")
	result.WriteString("- State: set_state and {{ State['key'] }}\n")
	result.WriteString("- Filters: | length, | default(''), | to_json, | from_json\n")
	result.WriteString("- Time functions: now(), | time_add('-1h'), | date_format('2006-01-02')\n")

	return core.NBToolResponse{Data: result.String(), Type: core.NBToolResponseTypeText}, nil
}

// --- Task Schema Lookup Tool ---

type TaskSchemaLookupTool struct{}

func (t TaskSchemaLookupTool) Name() string             { return ToolWorkflowTaskSchema }
func (t TaskSchemaLookupTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (t TaskSchemaLookupTool) Description() string {
	return "Gets the parameter schema for a specific task type. Use this to find valid parameters before building automation tasks."
}
func (t TaskSchemaLookupTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"task_type": {
				Type:        core.ToolSchemaTypeString,
				Description: "The task type to look up (e.g., 'k8s.cli', 'notifications.im')",
			},
		},
		Required: []string{"task_type"},
	}
}
func (t TaskSchemaLookupTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	taskType, ok := input.Arguments["task_type"].(string)
	if !ok {
		return core.NBToolResponse{}, errors.New("task_type parameter is required")
	}

	// Get all tasks from API
	resp, err := DoRunbookRequest("GET", "tasks", nil, ctx.AccountId, ctx.Ctx.GetSecurityContext().GetTenantId(), ctx.Ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return core.NBToolResponse{}, err
	}

	// Parse the tasks JSON to find the specific task type
	var tasks []map[string]interface{}
	if err := common.UnmarshalJson(resp, &tasks); err != nil {
		return core.NBToolResponse{}, fmt.Errorf("failed to parse tasks response: %w", err)
	}

	// Find the requested task type
	for _, task := range tasks {
		if task["type"] == taskType {
			// Return the task schema
			schemaBytes, err := common.MarshalJson(task)
			if err != nil {
				return core.NBToolResponse{}, err
			}
			return core.NBToolResponse{Data: string(schemaBytes), Type: core.NBToolResponseTypeJson}, nil
		}
	}

	return core.NBToolResponse{}, fmt.Errorf("task type '%s' not found", taskType)
}

// coerceWorkflowTypes walks a JSON map and converts float64 values that are
// whole numbers to int. Go's json.Unmarshal into map[string]interface{} always
// produces float64 for JSON numbers, but the workflow API expects integer types
// for fields like concurrency, max_retries, etc.
func coerceWorkflowTypes(data map[string]interface{}) {
	for key, val := range data {
		switch v := val.(type) {
		case float64:
			if v == float64(int(v)) {
				data[key] = int(v)
			}
		case map[string]interface{}:
			coerceWorkflowTypes(v)
		case []interface{}:
			for i, item := range v {
				switch itemVal := item.(type) {
				case float64:
					if itemVal == float64(int(itemVal)) {
						v[i] = int(itemVal)
					}
				case map[string]interface{}:
					coerceWorkflowTypes(itemVal)
				}
			}
		}
	}
}

// EnsureDefinitionObject ensures the definition is a map[string]interface{}.
// LLM agents sometimes pass the definition as a JSON string instead of an object;
// this causes DoRunbookRequest to double-encode it, resulting in unmarshal errors.
func EnsureDefinitionObject(definition interface{}) (interface{}, error) {
	if defStr, ok := definition.(string); ok {
		defStr = strings.TrimSpace(defStr)
		if defStr == "" {
			return nil, errors.New("definition is empty")
		}
		var defObj map[string]interface{}
		if err := json.Unmarshal([]byte(defStr), &defObj); err != nil {
			return nil, fmt.Errorf("definition is not valid JSON: %w", err)
		}
		definition = defObj
	}
	if defMap, ok := definition.(map[string]interface{}); ok {
		coerceWorkflowTypes(defMap)
	}
	return definition, nil
}

// --- Workflow Update Tool ---

type WorkflowUpdateTool struct{}

func (t WorkflowUpdateTool) Name() string             { return ToolWorkflowUpdate }
func (t WorkflowUpdateTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (t WorkflowUpdateTool) Description() string {
	return "Updates an existing automation. Pass the inner definition (with tasks, triggers, version) under 'definition'; the tool wraps it into the {name, definition} shape the server expects. Pass 'name' if you want to rename — otherwise the existing name is reused."
}
func (t WorkflowUpdateTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"id":         {Type: core.ToolSchemaTypeString, Description: "The ID of the automation to update"},
			"name":       {Type: core.ToolSchemaTypeString, Description: "Optional. Automation name. If omitted, the existing name is reused. Provide ONLY to rename."},
			"definition": {Type: core.ToolSchemaTypeObject, Description: "The inner definition object containing 'tasks', 'triggers', 'version', etc. Do NOT wrap it in another 'definition' key — the tool does that."},
		},
		Required: []string{"id", "definition"},
	}
}
func (t WorkflowUpdateTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	id, ok := input.Arguments["id"].(string)
	if !ok || id == "" {
		return core.NBToolResponse{}, errors.New("id is required")
	}

	definition, ok := input.Arguments["definition"]
	if !ok {
		return core.NBToolResponse{}, errors.New("definition is required")
	}

	// Ensure definition is an object (LLM may pass it as a JSON string)
	definition, err := EnsureDefinitionObject(definition)
	if err != nil {
		return core.NBToolResponse{}, fmt.Errorf("invalid definition: %w", err)
	}

	// Normalise to the {name, definition: {...}} wrapper the runbook server expects.
	// Accepts whatever shape the LLM produces (inner-only, full-wrapper, or flat).
	payload, err := NormaliseWorkflowUpdatePayload(ctx, id, input.Arguments, definition)
	if err != nil {
		return core.NBToolResponse{}, err
	}

	resp, err := DoRunbookRequest("PUT", fmt.Sprintf("workflows/%s", id), payload, ctx.AccountId, ctx.Ctx.GetSecurityContext().GetTenantId(), ctx.Ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return core.NBToolResponse{}, err
	}
	return core.NBToolResponse{Data: string(resp), Type: core.NBToolResponseTypeJson}, nil
}

// NormaliseWorkflowUpdatePayload reshapes whatever the LLM passes into the {name,
// definition: {...}} wrapper the runbook server expects on PUT /workflows/{id}.
//
// LLMs have been observed to produce three shapes — all of which previously caused
// validation failures because the tool forwarded the raw bytes:
//
//  1. Inner definition only:  {tasks, triggers, version, ...}
//  2. Full wrapper:           {name, definition: {tasks, triggers, ...}}
//  3. Flat (mistaken):        {name, tasks, triggers, ...}
//
// This helper detects which shape was sent and assembles the wrapper. If a name is
// not supplied via args["name"] or inside the payload, the existing workflow's name
// is fetched and reused — preserving the natural semantic that workflow_update is
// for editing the definition, not renaming.
func NormaliseWorkflowUpdatePayload(ctx core.NbToolContext, id string, args map[string]interface{}, definition interface{}) (interface{}, error) {
	defMap, ok := definition.(map[string]interface{})
	if !ok {
		// Not a map (rare); pass through and let the server validate.
		return definition, nil
	}

	explicitName, _ := args["name"].(string)

	// Shape 2: caller already wrapped — has a nested "definition" object.
	if nested, hasNested := defMap["definition"]; hasNested {
		if _, isMap := nested.(map[string]interface{}); isMap {
			if name, ok := defMap["name"].(string); !ok || name == "" {
				if explicitName != "" {
					defMap["name"] = explicitName
				} else if fetched, fErr := fetchWorkflowName(ctx, id); fErr == nil && fetched != "" {
					defMap["name"] = fetched
				}
			}
			return defMap, nil
		}
	}

	// Shape 3: flat — has BOTH "name" and "tasks" at the top level.
	if name, hasName := defMap["name"].(string); hasName && name != "" {
		if _, hasTasks := defMap["tasks"]; hasTasks {
			inner := make(map[string]interface{}, len(defMap)-1)
			for k, v := range defMap {
				if k != "name" {
					inner[k] = v
				}
			}
			return map[string]interface{}{"name": name, "definition": inner}, nil
		}
	}

	// Shape 1: inner-only. Build the wrapper using explicit name or fetched name.
	name := explicitName
	if name == "" {
		fetched, fErr := fetchWorkflowName(ctx, id)
		if fErr != nil {
			return nil, fmt.Errorf("workflow_update: 'name' not provided and could not fetch existing workflow %s: %w", id, fErr)
		}
		name = fetched
	}
	if name == "" {
		return nil, errors.New("workflow_update: 'name' is required and could not be inferred from the existing automation")
	}
	return map[string]interface{}{"name": name, "definition": defMap}, nil
}

// fetchWorkflowName fetches the existing workflow's "name" so workflow_update can
// preserve it when the LLM doesn't provide one.
func fetchWorkflowName(ctx core.NbToolContext, id string) (string, error) {
	resp, err := DoRunbookRequest("GET", fmt.Sprintf("workflows/%s", id), nil, ctx.AccountId, ctx.Ctx.GetSecurityContext().GetTenantId(), ctx.Ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return "", err
	}
	var wf map[string]interface{}
	if err := json.Unmarshal(resp, &wf); err != nil {
		return "", fmt.Errorf("parse workflow response: %w", err)
	}
	name, _ := wf["name"].(string)
	return name, nil
}

// --- Workflow Executions Tool ---

type WorkflowExecutionsTool struct{}

func (t WorkflowExecutionsTool) Name() string             { return ToolWorkflowExecutions }
func (t WorkflowExecutionsTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (t WorkflowExecutionsTool) Description() string {
	return "Lists executions (runs) of an automation with optional filtering by status."
}
func (t WorkflowExecutionsTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"id":     {Type: core.ToolSchemaTypeString, Description: "The ID of the automation"},
			"limit":  {Type: core.ToolSchemaTypeString, Description: "Number of executions to return (default 20)"},
			"status": {Type: core.ToolSchemaTypeString, Description: "Filter by execution status (e.g., FAILED, COMPLETED, RUNNING)"},
		},
		Required: []string{"id"},
	}
}
func (t WorkflowExecutionsTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	id, ok := input.Arguments["id"].(string)
	if !ok || id == "" {
		return core.NBToolResponse{}, errors.New("id is required")
	}

	queryParams := []string{}
	if val, ok := input.Arguments["limit"]; ok {
		queryParams = append(queryParams, fmt.Sprintf("limit=%v", val))
	}
	if val, ok := input.Arguments["status"]; ok {
		queryParams = append(queryParams, fmt.Sprintf("status=%v", val))
	}

	path := fmt.Sprintf("workflows/%s/runs", id)
	if len(queryParams) > 0 {
		path += "?" + strings.Join(queryParams, "&")
	}

	resp, err := DoRunbookRequest("GET", path, nil, ctx.AccountId, ctx.Ctx.GetSecurityContext().GetTenantId(), ctx.Ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return core.NBToolResponse{}, err
	}
	return core.NBToolResponse{Data: string(resp), Type: core.NBToolResponseTypeJson}, nil
}

// --- Workflow Execution Get Tool ---

type WorkflowExecutionGetTool struct{}

func (t WorkflowExecutionGetTool) Name() string             { return ToolWorkflowExecutionGet }
func (t WorkflowExecutionGetTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (t WorkflowExecutionGetTool) Description() string {
	return "Gets details of a specific automation execution including task-level errors, inputs, and outputs."
}
func (t WorkflowExecutionGetTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"id":           {Type: core.ToolSchemaTypeString, Description: "The ID of the automation"},
			"execution_id": {Type: core.ToolSchemaTypeString, Description: "The ID of the execution to retrieve"},
		},
		Required: []string{"id", "execution_id"},
	}
}
func (t WorkflowExecutionGetTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	id, ok := input.Arguments["id"].(string)
	if !ok || id == "" {
		return core.NBToolResponse{}, errors.New("id is required")
	}
	executionId, ok := input.Arguments["execution_id"].(string)
	if !ok || executionId == "" {
		return core.NBToolResponse{}, errors.New("execution_id is required")
	}

	resp, err := DoRunbookRequest("GET", fmt.Sprintf("workflows/%s/runs/%s", id, executionId), nil, ctx.AccountId, ctx.Ctx.GetSecurityContext().GetTenantId(), ctx.Ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return core.NBToolResponse{}, err
	}
	return core.NBToolResponse{Data: string(resp), Type: core.NBToolResponseTypeJson}, nil
}

// --- Workflow Execution Retrigger Tool ---

type WorkflowExecutionRetriggerTool struct{}

func (t WorkflowExecutionRetriggerTool) Name() string             { return ToolWorkflowExecutionRetrigger }
func (t WorkflowExecutionRetriggerTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (t WorkflowExecutionRetriggerTool) Description() string {
	return "Re-triggers an automation execution. Useful for retrying a failed execution."
}
func (t WorkflowExecutionRetriggerTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"id":           {Type: core.ToolSchemaTypeString, Description: "The ID of the automation"},
			"execution_id": {Type: core.ToolSchemaTypeString, Description: "The ID of the execution to retrigger"},
			"inputs":       {Type: core.ToolSchemaTypeObject, Description: "Optional input parameters for the new execution"},
		},
		Required: []string{"id", "execution_id"},
	}
}
func (t WorkflowExecutionRetriggerTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	id, ok := input.Arguments["id"].(string)
	if !ok || id == "" {
		return core.NBToolResponse{}, errors.New("id is required")
	}
	executionId, ok := input.Arguments["execution_id"].(string)
	if !ok || executionId == "" {
		return core.NBToolResponse{}, errors.New("execution_id is required")
	}

	body := input.Arguments["inputs"]
	if body == nil {
		body = map[string]any{}
	}

	resp, err := DoRunbookRequest("POST", fmt.Sprintf("workflows/%s/runs/%s/retrigger", id, executionId), body, ctx.AccountId, ctx.Ctx.GetSecurityContext().GetTenantId(), ctx.Ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return core.NBToolResponse{}, err
	}
	return core.NBToolResponse{Data: string(resp), Type: core.NBToolResponseTypeJson}, nil
}

// --- Workflow State Tool ---

type WorkflowStateTool struct{}

func (t WorkflowStateTool) Name() string             { return ToolWorkflowState }
func (t WorkflowStateTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (t WorkflowStateTool) Description() string {
	return "Gets the persistent state of an automation (key-value store that persists across runs)."
}
func (t WorkflowStateTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"id": {Type: core.ToolSchemaTypeString, Description: "The ID of the automation"},
		},
		Required: []string{"id"},
	}
}
func (t WorkflowStateTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	id, ok := input.Arguments["id"].(string)
	if !ok || id == "" {
		return core.NBToolResponse{}, errors.New("id is required")
	}

	resp, err := DoRunbookRequest("GET", fmt.Sprintf("workflows/%s/state", id), nil, ctx.AccountId, ctx.Ctx.GetSecurityContext().GetTenantId(), ctx.Ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return core.NBToolResponse{}, err
	}
	return core.NBToolResponse{Data: string(resp), Type: core.NBToolResponseTypeJson}, nil
}

// --- Workflow Config List Tool ---

type WorkflowConfigListTool struct{}

func (t WorkflowConfigListTool) Name() string             { return ToolWorkflowConfigList }
func (t WorkflowConfigListTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (t WorkflowConfigListTool) Description() string {
	return "Lists automation configs with optional label filtering. Secret values are masked."
}
func (t WorkflowConfigListTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"labels": {Type: core.ToolSchemaTypeObject, Description: "Optional label filters as key-value pairs (e.g., {\"env\": \"prod\"})"},
		},
	}
}
func (t WorkflowConfigListTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	path := "configs"

	if labels, ok := input.Arguments["labels"].(map[string]interface{}); ok && len(labels) > 0 {
		queryParams := []string{}
		for k, v := range labels {
			queryParams = append(queryParams, fmt.Sprintf("labels[%s]=%v", k, v))
		}
		path += "?" + strings.Join(queryParams, "&")
	}

	resp, err := DoRunbookRequest("GET", path, nil, ctx.AccountId, ctx.Ctx.GetSecurityContext().GetTenantId(), ctx.Ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return core.NBToolResponse{}, err
	}
	return core.NBToolResponse{Data: string(resp), Type: core.NBToolResponseTypeJson}, nil
}

// --- Workflow Config Get Tool ---

type WorkflowConfigGetTool struct{}

func (t WorkflowConfigGetTool) Name() string             { return ToolWorkflowConfigGet }
func (t WorkflowConfigGetTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (t WorkflowConfigGetTool) Description() string {
	return "Gets a specific automation config by key. Secret values are masked."
}
func (t WorkflowConfigGetTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"key": {Type: core.ToolSchemaTypeString, Description: "The config key to retrieve"},
		},
		Required: []string{"key"},
	}
}
func (t WorkflowConfigGetTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	key, ok := input.Arguments["key"].(string)
	if !ok || key == "" {
		return core.NBToolResponse{}, errors.New("key is required")
	}

	resp, err := DoRunbookRequest("GET", fmt.Sprintf("configs/%s", key), nil, ctx.AccountId, ctx.Ctx.GetSecurityContext().GetTenantId(), ctx.Ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return core.NBToolResponse{}, err
	}
	return core.NBToolResponse{Data: string(resp), Type: core.NBToolResponseTypeJson}, nil
}

// --- Workflow Config Save Tool ---

type WorkflowConfigSaveTool struct{}

func (t WorkflowConfigSaveTool) Name() string             { return ToolWorkflowConfigSave }
func (t WorkflowConfigSaveTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (t WorkflowConfigSaveTool) Description() string {
	return "Creates or updates an automation config. Secret values are encrypted at rest."
}
func (t WorkflowConfigSaveTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"key":   {Type: core.ToolSchemaTypeString, Description: "The config key"},
			"value": {Type: core.ToolSchemaTypeString, Description: "The config value"},
			"type":  {Type: core.ToolSchemaTypeString, Description: "Config type: 'config' (default) or 'secret'"},
		},
		Required: []string{"key", "value"},
	}
}
func (t WorkflowConfigSaveTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	key, ok := input.Arguments["key"].(string)
	if !ok || key == "" {
		return core.NBToolResponse{}, errors.New("key is required")
	}
	value, ok := input.Arguments["value"].(string)
	if !ok {
		return core.NBToolResponse{}, errors.New("value is required")
	}
	configType := "config"
	if t, ok := input.Arguments["type"].(string); ok && t != "" {
		configType = t
	}

	body := map[string]string{
		"key":   key,
		"value": value,
		"type":  configType,
	}

	resp, err := DoRunbookRequest("POST", "configs", body, ctx.AccountId, ctx.Ctx.GetSecurityContext().GetTenantId(), ctx.Ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return core.NBToolResponse{}, err
	}
	return core.NBToolResponse{Data: string(resp), Type: core.NBToolResponseTypeJson}, nil
}
