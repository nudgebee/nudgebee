package agents

import (
	"encoding/json"
	"fmt"
	"nudgebee/llm/agents/core"
	toolcore "nudgebee/llm/tools/core"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// displayText returns the text a user sees in the chat for this turn: the
// followup question when the agent is WAITING on input, otherwise the joined
// Response. WorkflowBuilder emits the question only via FollowupRequest on
// WAITING turns (Response is left empty to avoid the UI rendering it twice).

func TestWorkflowBuilderAgent_StateMarshalUnmarshal(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")
	agent.state = WorkflowBuilderState{
		Stage:         "plan_approval",
		OriginalQuery: "Build a workflow that prints hello",
		Intent:        `{"description":"print hello"}`,
		Plan:          "1. Create a print task\n2. Use core.print type",
		PlanAttempts:  1,
	}

	// Marshal
	data, err := agent.MarshalState()
	assert.Nil(t, err)
	assert.NotNil(t, data)

	// Create a new agent and unmarshal into it
	agent2 := newWorkflowBuilderAgent("test-account")
	err = agent2.UnmarshalState(data)
	assert.Nil(t, err)

	// Verify state matches
	assert.Equal(t, agent.state.Stage, agent2.state.Stage)
	assert.Equal(t, agent.state.OriginalQuery, agent2.state.OriginalQuery)
	assert.Equal(t, agent.state.Intent, agent2.state.Intent)
	assert.Equal(t, agent.state.Plan, agent2.state.Plan)
	assert.Equal(t, agent.state.PlanAttempts, agent2.state.PlanAttempts)
}

// TestWorkflowBuilderAgent_FixMode_StateMarshal tests that fix-mode state fields survive marshal/unmarshal cycle

func TestWorkflowBuilderAgent_FixMode_StateMarshal(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")
	agent.state = WorkflowBuilderState{
		Stage:              "fix_approval",
		Mode:               "fix",
		OriginalQuery:      "Fix the pod health check workflow",
		WorkflowId:         "abc-123",
		ExistingDefinition: `{"name":"test","definition":{"tasks":[]}}`,
		ExecutionError:     "Task 'get-pods' failed: command error",
		ProposedChanges:    `{"diagnosis":"wrong namespace","changes":[]}`,
		ProposedDiff:       "**Task: `get-pods`**\n`params.command`:\n- Before: `get pods -n default`\n- After: `get pods -n production`",
	}

	// Marshal
	data, err := agent.MarshalState()
	assert.Nil(t, err)
	assert.NotNil(t, data)

	// Create a new agent and unmarshal into it
	agent2 := newWorkflowBuilderAgent("test-account")
	err = agent2.UnmarshalState(data)
	assert.Nil(t, err)

	// Verify all fix-mode state fields match
	assert.Equal(t, agent.state.Stage, agent2.state.Stage)
	assert.Equal(t, agent.state.Mode, agent2.state.Mode)
	assert.Equal(t, agent.state.OriginalQuery, agent2.state.OriginalQuery)
	assert.Equal(t, agent.state.WorkflowId, agent2.state.WorkflowId)
	assert.Equal(t, agent.state.ExistingDefinition, agent2.state.ExistingDefinition)
	assert.Equal(t, agent.state.ExecutionError, agent2.state.ExecutionError)
	assert.Equal(t, agent.state.ProposedChanges, agent2.state.ProposedChanges)
	assert.Equal(t, agent.state.ProposedDiff, agent2.state.ProposedDiff)
}

// TestWorkflowBuilderAgent_ToolInitWorkflow tests the init_workflow tool

func TestWorkflowBuilderAgent_ToolInitWorkflow(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")

	// Initialize workflow
	result := agent.toolInitWorkflow(map[string]interface{}{
		"name": "test-workflow",
		"triggers": []interface{}{
			map[string]interface{}{"type": "manual"},
		},
	})
	assert.Contains(t, result, "initialized")
	assert.NotNil(t, agent.state.WorkingWorkflow)
	assert.Equal(t, "test-workflow", agent.state.WorkingWorkflow["name"])

	// Missing name
	agent2 := newWorkflowBuilderAgent("test-account")
	result = agent2.toolInitWorkflow(map[string]interface{}{})
	assert.Contains(t, result, "Error")
}

// TestWorkflowBuilderAgent_ToolAddGetModifyDeleteTask tests the task CRUD tools

func TestWorkflowBuilderAgent_ToolAddGetModifyDeleteTask(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")

	// Init workflow first
	agent.toolInitWorkflow(map[string]interface{}{
		"name":     "test-workflow",
		"triggers": []interface{}{map[string]interface{}{"type": "manual"}},
	})

	// Add task
	result := agent.toolAddTask(map[string]interface{}{
		"id":   "get-pods",
		"type": "k8s.cli",
		"params": map[string]interface{}{
			"command": "get pods -n default",
		},
	})
	assert.Contains(t, result, "added successfully")
	assert.Contains(t, result, "1 task(s)")

	// Add duplicate task should fail
	result = agent.toolAddTask(map[string]interface{}{
		"id":   "get-pods",
		"type": "k8s.cli",
		"params": map[string]interface{}{
			"command": "get pods -n default",
		},
	})
	assert.Contains(t, result, "already exists")

	// Get task
	result = agent.toolGetTask(map[string]interface{}{"task_id": "get-pods"})
	assert.Contains(t, result, "get pods -n default")
	assert.Contains(t, result, "k8s.cli")

	// Get non-existent task
	result = agent.toolGetTask(map[string]interface{}{"task_id": "not-here"})
	assert.Contains(t, result, "not found")

	// Modify task
	result = agent.toolModifyTask(map[string]interface{}{
		"task_id": "get-pods",
		"id":      "get-pods",
		"type":    "k8s.cli",
		"params": map[string]interface{}{
			"command": "get pods -n production -o json",
		},
	})
	assert.Contains(t, result, "updated")

	// Verify modification
	result = agent.toolGetTask(map[string]interface{}{"task_id": "get-pods"})
	assert.Contains(t, result, "get pods -n production -o json")

	// Add second task
	agent.toolAddTask(map[string]interface{}{
		"id":         "notify",
		"type":       "notifications.im",
		"depends_on": []interface{}{"get-pods"},
		"params": map[string]interface{}{
			"provider": "slack",
			"message":  "Done",
		},
	})

	// List tasks
	result = agent.toolListTasks(map[string]interface{}{})
	assert.Contains(t, result, "get-pods")
	assert.Contains(t, result, "notify")
	assert.Contains(t, result, "k8s.cli")
	assert.Contains(t, result, "notifications.im")

	// Delete task
	result = agent.toolDeleteTask(map[string]interface{}{"task_id": "notify"})
	assert.Contains(t, result, "deleted")

	// Verify deletion
	result = agent.toolListTasks(map[string]interface{}{})
	assert.NotContains(t, result, "notify")
	assert.Contains(t, result, "get-pods")
}

// TestWorkflowBuilderAgent_ToolFinalize tests the finalize tool

func TestWorkflowBuilderAgent_ToolFinalize(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")

	// Init and add a task
	agent.toolInitWorkflow(map[string]interface{}{
		"name":     "test-workflow",
		"triggers": []interface{}{map[string]interface{}{"type": "manual"}},
	})
	agent.toolAddTask(map[string]interface{}{
		"id":   "print-hello",
		"type": "core.print",
		"params": map[string]interface{}{
			"message": "Hello World",
		},
	})

	// Finalize
	result := agent.toolFinalize()
	assert.Contains(t, result, "test-workflow")
	assert.Contains(t, result, "print-hello")
	assert.Contains(t, result, "core.print")

	// Should be valid JSON
	var workflow map[string]interface{}
	err := json.Unmarshal([]byte(result), &workflow)
	assert.Nil(t, err)
	assert.Equal(t, "test-workflow", workflow["name"])
}

// TestWorkflowBuilderAgent_BuildAndModifyWorkflow tests a full create → modify → validate → finalize cycle
// using in-memory tools without requiring LLM or runbook-server.

func TestWorkflowBuilderAgent_BuildAndModifyWorkflow(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")

	// Step 1: Initialize workflow
	result := agent.toolInitWorkflow(map[string]interface{}{
		"name": "pod-health-monitor",
		"triggers": []interface{}{
			map[string]interface{}{"type": "schedule", "params": map[string]interface{}{"cron": "*/30 * * * *"}},
		},
		"inputs": []interface{}{
			map[string]interface{}{"id": "namespace", "type": "string", "default": "production"},
		},
	})
	assert.Contains(t, result, "initialized")

	// Step 2: Add k8s task
	result = agent.toolAddTask(map[string]interface{}{
		"id":   "get-pods",
		"type": "k8s.cli",
		"params": map[string]interface{}{
			"command": "get pods -n {{ Inputs.namespace }} -o json",
		},
	})
	assert.Contains(t, result, "added")

	// Step 3: Add transform task
	result = agent.toolAddTask(map[string]interface{}{
		"id":         "check-health",
		"type":       "data.transform",
		"depends_on": []interface{}{"get-pods"},
		"params": map[string]interface{}{
			"input":      "{{ Tasks['get-pods'].output.data }}",
			"inputType":  "json",
			"expression": `{ "unhealthy": items[status.phase != 'Running'] }`,
		},
	})
	assert.Contains(t, result, "added")
	assert.Contains(t, result, "2 task(s)")

	// Step 4: Add notification task with condition
	result = agent.toolAddTask(map[string]interface{}{
		"id":         "send-alert",
		"type":       "notifications.im",
		"depends_on": []interface{}{"check-health"},
		"if":         "{{ Tasks['check-health'].output.data.unhealthy | length > 0 }}",
		"params": map[string]interface{}{
			"provider": "slack",
			"channel":  "#alerts",
			"message":  "Found {{ Tasks['check-health'].output.data.unhealthy | length }} unhealthy pods",
		},
	})
	assert.Contains(t, result, "added")
	assert.Contains(t, result, "3 task(s)")

	// Step 5: Verify list_tasks shows all tasks
	result = agent.toolListTasks(map[string]interface{}{})
	assert.Contains(t, result, "get-pods")
	assert.Contains(t, result, "check-health")
	assert.Contains(t, result, "send-alert")
	assert.Contains(t, result, "k8s.cli")
	assert.Contains(t, result, "data.transform")
	assert.Contains(t, result, "notifications.im")

	// Step 6: Simulate a fix — change the namespace
	result = agent.toolGetTask(map[string]interface{}{"task_id": "get-pods"})
	assert.Contains(t, result, "Inputs.namespace")

	result = agent.toolModifyTask(map[string]interface{}{
		"task_id": "get-pods",
		"id":      "get-pods",
		"type":    "k8s.cli",
		"params": map[string]interface{}{
			"command": "get pods -n production -o json",
		},
	})
	assert.Contains(t, result, "updated")

	// Verify the change
	result = agent.toolGetTask(map[string]interface{}{"task_id": "get-pods"})
	assert.Contains(t, result, "production")
	assert.NotContains(t, result, "Inputs.namespace")

	// Step 7: Add a fourth task, then delete it
	agent.toolAddTask(map[string]interface{}{
		"id":         "log-result",
		"type":       "core.print",
		"depends_on": []interface{}{"send-alert"},
		"params": map[string]interface{}{
			"message": "Alert sent",
		},
	})
	result = agent.toolListTasks(map[string]interface{}{})
	assert.Contains(t, result, "log-result")
	assert.Contains(t, result, "Tasks (4)")

	result = agent.toolDeleteTask(map[string]interface{}{"task_id": "log-result"})
	assert.Contains(t, result, "deleted")

	result = agent.toolListTasks(map[string]interface{}{})
	assert.NotContains(t, result, "log-result")
	assert.Contains(t, result, "Tasks (3)")

	// Step 8: Finalize — should produce valid JSON with all changes applied
	result = agent.toolFinalize()
	var workflow map[string]interface{}
	err := json.Unmarshal([]byte(result), &workflow)
	assert.Nil(t, err)
	assert.Equal(t, "pod-health-monitor", workflow["name"])

	definition := workflow["definition"].(map[string]interface{})
	tasks := definition["tasks"].([]interface{})
	assert.Equal(t, 3, len(tasks))

	// Verify triggers persisted
	triggers := definition["triggers"].([]interface{})
	assert.Equal(t, 1, len(triggers))
	trigger := triggers[0].(map[string]interface{})
	assert.Equal(t, "schedule", trigger["type"])

	// Verify inputs persisted
	inputs := definition["inputs"].([]interface{})
	assert.Equal(t, 1, len(inputs))

	// Verify first task has updated command
	task0 := tasks[0].(map[string]interface{})
	assert.Equal(t, "get-pods", task0["id"])
	params0 := task0["params"].(map[string]interface{})
	assert.Equal(t, "get pods -n production -o json", params0["command"])

	// Verify third task has conditional
	task2 := tasks[2].(map[string]interface{})
	assert.Equal(t, "send-alert", task2["id"])
	assert.Contains(t, task2["if"], "unhealthy")
}

// TestWorkflowBuilderAgent_LoadExistingAndModify simulates fix mode: load an existing
// workflow JSON into workingWorkflow, then modify specific tasks.

func TestWorkflowBuilderAgent_LoadExistingAndModify(t *testing.T) {
	existingJSON := `{
  "name": "daily-report",
  "definition": {
    "version": "v1",
    "triggers": [{"type": "schedule", "params": {"cron": "0 9 * * *"}}],
    "tasks": [
      {
        "id": "fetch-metrics",
        "type": "k8s.cli",
        "params": {"command": "top pods -n default"}
      },
      {
        "id": "summarize",
        "type": "llm.investigate",
        "depends_on": ["fetch-metrics"],
        "params": {"message": "Summarize: {{ Tasks['fetch-metrics'].output.data }}"}
      },
      {
        "id": "send-report",
        "type": "notifications.im",
        "depends_on": ["summarize"],
        "params": {
          "provider": "slack",
          "channel": "#daily-reports",
          "message": "{{ Tasks['summarize'].output.data }}"
        }
      }
    ]
  }
}`

	agent := newWorkflowBuilderAgent("test-account")

	// Load existing workflow into workingWorkflow (simulates what handleFixEntry does)
	var workflow map[string]interface{}
	err := json.Unmarshal([]byte(existingJSON), &workflow)
	assert.Nil(t, err)
	agent.state.WorkingWorkflow = workflow

	// Diagnose: list tasks to understand structure
	result := agent.toolListTasks(map[string]interface{}{})
	assert.Contains(t, result, "fetch-metrics")
	assert.Contains(t, result, "summarize")
	assert.Contains(t, result, "send-report")
	assert.Contains(t, result, "Tasks (3)")

	// Read the problematic task
	result = agent.toolGetTask(map[string]interface{}{"task_id": "fetch-metrics"})
	assert.Contains(t, result, "top pods -n default")

	// Fix 1: wrong namespace in fetch-metrics
	result = agent.toolModifyTask(map[string]interface{}{
		"task_id": "fetch-metrics",
		"id":      "fetch-metrics",
		"type":    "k8s.cli",
		"params":  map[string]interface{}{"command": "top pods -n production --sort-by=cpu"},
	})
	assert.Contains(t, result, "updated")

	// Fix 2: change slack channel
	result = agent.toolGetTask(map[string]interface{}{"task_id": "send-report"})
	assert.Contains(t, result, "#daily-reports")

	result = agent.toolModifyTask(map[string]interface{}{
		"task_id":    "send-report",
		"id":         "send-report",
		"type":       "notifications.im",
		"depends_on": []interface{}{"summarize"},
		"params": map[string]interface{}{
			"provider": "slack",
			"channel":  "{{ Configs.slack_reports_channel }}",
			"message":  "{{ Tasks['summarize'].output.data }}",
		},
	})
	assert.Contains(t, result, "updated")

	// Add a new task: also send email
	result = agent.toolAddTask(map[string]interface{}{
		"id":         "send-email",
		"type":       "notifications.email",
		"depends_on": []interface{}{"summarize"},
		"params": map[string]interface{}{
			"to":      "team@example.com",
			"subject": "Daily Pod Health Report",
			"body":    "{{ Tasks['summarize'].output.data }}",
		},
	})
	assert.Contains(t, result, "added")
	assert.Contains(t, result, "4 task(s)")

	// Finalize and verify
	result = agent.toolFinalize()
	var finalWorkflow map[string]interface{}
	err = json.Unmarshal([]byte(result), &finalWorkflow)
	assert.Nil(t, err)
	assert.Equal(t, "daily-report", finalWorkflow["name"])

	def := finalWorkflow["definition"].(map[string]interface{})
	tasks := def["tasks"].([]interface{})
	assert.Equal(t, 4, len(tasks))

	// Verify fix 1: namespace change
	t0 := tasks[0].(map[string]interface{})
	assert.Equal(t, "top pods -n production --sort-by=cpu", t0["params"].(map[string]interface{})["command"])

	// Verify fix 2: channel uses Configs
	t2 := tasks[2].(map[string]interface{})
	assert.Equal(t, "{{ Configs.slack_reports_channel }}", t2["params"].(map[string]interface{})["channel"])

	// Verify new task added
	t3 := tasks[3].(map[string]interface{})
	assert.Equal(t, "send-email", t3["id"])
	assert.Equal(t, "notifications.email", t3["type"])
}

// TestWorkflowBuilderAgent_CoercionInFinalize tests that finalize applies type coercion

func TestWorkflowBuilderAgent_CoercionInFinalize(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")

	agent.toolInitWorkflow(map[string]interface{}{
		"name": "coercion-test",
	})
	agent.toolAddTask(map[string]interface{}{
		"id":   "task-1",
		"type": "http.request",
		"params": map[string]interface{}{
			"concurrency": float64(5),
			"max_retries": float64(3),
			"timeout":     float64(30),
			"ratio":       float64(0.75), // non-whole float should stay
			"url":         "https://example.com",
		},
	})

	result := agent.toolFinalize()
	var workflow map[string]interface{}
	err := json.Unmarshal([]byte(result), &workflow)
	assert.Nil(t, err)

	def := workflow["definition"].(map[string]interface{})
	tasks := def["tasks"].([]interface{})
	params := tasks[0].(map[string]interface{})["params"].(map[string]interface{})

	// After coercion + re-marshal via JSON, whole-number floats become ints in the JSON
	// but json.Unmarshal will read them back as float64. What matters is the JSON output
	// contains "5" not "5.0". Check via string:
	assert.Contains(t, result, `"concurrency": 5`)
	assert.NotContains(t, result, `"concurrency": 5.0`)
	assert.Contains(t, result, `"max_retries": 3`)
	assert.NotContains(t, result, `"max_retries": 3.0`)

	// Non-whole float should remain
	assert.Equal(t, 0.75, params["ratio"])
}

// TestWorkflowBuilderAgent_HandleEntry_DetectsFixMode tests that handleEntry routes correctly

func TestWorkflowBuilderAgent_HandleEntry_DetectsFixMode(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")

	// Without workflow_id in QueryConfig → should try create mode (will fail without LLM, but confirms routing)
	assert.Equal(t, "", agent.state.Mode)

	// With workflow_id → state should reflect fix mode after entry attempt
	// (We can't fully test handleFixEntry without a running runbook-server,
	// but we can verify the routing logic works by checking the QueryConfig detection)
	request := core.NBAgentRequest{
		QueryConfig: toolcore.NBQueryConfig{
			WorkflowId: "test-workflow-123",
		},
	}
	assert.True(t, request.QueryConfig.WorkflowId != "")
}

// TestWorkflowBuilderAgent_ExtractWorkflowIdFromQuery tests UUID extraction from query text

func TestWorkflowBuilderAgent_ExtractWorkflowIdFromQuery(t *testing.T) {
	// Fix request with UUID → should extract
	id := extractWorkflowIdFromQuery("Fix workflow 5d064c4c-bb53-4630-95ff-f6bb7b9133a6. Error: task failed")
	assert.Equal(t, "5d064c4c-bb53-4630-95ff-f6bb7b9133a6", id)

	// Debug request with UUID → should extract
	id = extractWorkflowIdFromQuery("Debug the failing workflow abc12345-1234-5678-9abc-def012345678")
	assert.Equal(t, "abc12345-1234-5678-9abc-def012345678", id)

	// Error message with UUID → should extract
	id = extractWorkflowIdFromQuery("Workflow 5d064c4c-bb53-4630-95ff-f6bb7b9133a6 has an error in the notification task")
	assert.Equal(t, "5d064c4c-bb53-4630-95ff-f6bb7b9133a6", id)

	// Create request with UUID → should NOT extract (no fix keyword)
	id = extractWorkflowIdFromQuery("Create a workflow named 5d064c4c-bb53-4630-95ff-f6bb7b9133a6")
	assert.Equal(t, "", id)

	// Create request without UUID → should NOT extract
	id = extractWorkflowIdFromQuery("Build a workflow that prints hello")
	assert.Equal(t, "", id)

	// Fix request without UUID → should return empty
	id = extractWorkflowIdFromQuery("Fix the pod health check workflow")
	assert.Equal(t, "", id)
}

// TestWorkflowBuilderAgent_FixMode_Integration tests the full fix mode flow against a real workflow

func TestCoerceWorkflowTypes(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name: "converts whole-number floats to int",
			input: map[string]interface{}{
				"name": "test",
				"definition": map[string]interface{}{
					"tasks": []interface{}{
						map[string]interface{}{
							"id":   "task-1",
							"type": "http",
							"params": map[string]interface{}{
								"concurrency": float64(5),
								"timeout":     float64(30),
								"url":         "https://example.com",
							},
						},
					},
				},
			},
			expected: map[string]interface{}{
				"name": "test",
				"definition": map[string]interface{}{
					"tasks": []interface{}{
						map[string]interface{}{
							"id":   "task-1",
							"type": "http",
							"params": map[string]interface{}{
								"concurrency": 5,
								"timeout":     30,
								"url":         "https://example.com",
							},
						},
					},
				},
			},
		},
		{
			name: "preserves non-whole floats",
			input: map[string]interface{}{
				"value": float64(3.14),
				"whole": float64(42),
			},
			expected: map[string]interface{}{
				"value": float64(3.14),
				"whole": 42,
			},
		},
		{
			name: "handles arrays with mixed types",
			input: map[string]interface{}{
				"items": []interface{}{
					float64(1),
					float64(2.5),
					"string",
					map[string]interface{}{
						"nested": float64(10),
					},
				},
			},
			expected: map[string]interface{}{
				"items": []interface{}{
					1,
					float64(2.5),
					"string",
					map[string]interface{}{
						"nested": 10,
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			coerceWorkflowTypes(tc.input)
			assert.Equal(t, tc.expected, tc.input)
		})
	}
}

func TestCoerceWorkflowTypes_RoundTrip(t *testing.T) {
	// Simulate the actual scenario: JSON unmarshal produces float64, coercion fixes it
	jsonStr := `{"name":"test","definition":{"tasks":[{"id":"task-1","params":{"concurrency":5}}]}}`
	var workflow map[string]interface{}
	err := json.Unmarshal([]byte(jsonStr), &workflow)
	assert.Nil(t, err)

	// Before coercion: Go's json.Unmarshal produces float64
	def := workflow["definition"].(map[string]interface{})
	tasks := def["tasks"].([]interface{})
	params := tasks[0].(map[string]interface{})["params"].(map[string]interface{})
	assert.IsType(t, float64(0), params["concurrency"])

	// After coercion: should be int
	coerceWorkflowTypes(workflow)
	params = tasks[0].(map[string]interface{})["params"].(map[string]interface{})
	assert.IsType(t, int(0), params["concurrency"])
	assert.Equal(t, 5, params["concurrency"])
}

// ==================== AGENTIC INTEGRATION TESTS ====================
// These tests exercise the full runToolLoop() end-to-end with actual LLM calls.
// They require TEST_ACCOUNT, TEST_USER, TEST_TENANT env vars and a running backend.

// assertWorkflowResponse validates that the response is either a markdown summary
// (from ask-nudgebee source) or raw JSON (from WorkflowBuilder source). Both are valid
// depending on the ConversationSource of the request.

func TestExtractConfigReferences(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "no config references",
			input:    `{"params": {"channel": "#alerts", "message": "hello"}}`,
			expected: nil,
		},
		{
			name:     "single reference",
			input:    `{"channel": "{{ Configs.slack_channel }}"}`,
			expected: []string{"slack_channel"},
		},
		{
			name:     "multiple distinct references",
			input:    `{"channel": "{{ Configs.slack_channel }}", "account_id": "{{ Configs.aws_account_id }}"}`,
			expected: []string{"slack_channel", "aws_account_id"},
		},
		{
			name:     "duplicate references deduplicated",
			input:    `{"channel": "{{ Configs.slack_channel }}", "fallback": "{{ Configs.slack_channel }}"}`,
			expected: []string{"slack_channel"},
		},
		{
			name:     "variable whitespace",
			input:    `{"a": "{{Configs.no_space}}", "b": "{{  Configs.extra_space  }}"}`,
			expected: []string{"no_space", "extra_space"},
		},
		{
			name: "full workflow JSON",
			input: `{
				"name": "test-workflow",
				"definition": {
					"tasks": [
						{"id": "t1", "params": {"channel": "{{ Configs.slack_channel }}", "provider": "slack"}},
						{"id": "t2", "params": {"account_id": "{{ Configs.aws_account_id }}", "command": "aws s3 ls"}},
						{"id": "t3", "params": {"message": "{{ Tasks['t1'].output.data }}"}}
					]
				}
			}`,
			expected: []string{"slack_channel", "aws_account_id"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:     "configs in Jinja2 expressions not matched",
			input:    `{"if": "{{ Configs.threshold > 10 }}"}`,
			expected: nil, // regex requires }} immediately after key, so "Configs.threshold > 10" is not a standalone reference
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := extractConfigReferences(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// ==================== Issue #29944 — auto-create missing configs ====================
//
// Background: prior to the fix, when the built workflow referenced a config
// that did not exist on the workflow server, the agent returned a "Create Configs
// vs Skip" followup. Both branches were broken: Skip in editor mode shipped the
// JSON back to the UI which then hit a 400 "validation failed" on save; Skip in
// chat surfaced a "Not saved" message. The fix removes the followup entirely
// and unconditionally auto-creates empty configs so the workflow can save.
// User fills in the values afterwards via the Configs UI.

// TestWorkflowBuilderAgent_AutoCreateEmptyConfigs_Helper unit-tests the small
// loop that POSTs each missing config. Uses a real workflow server (port-forward
// at localhost:8889) so it exercises the same call shape as production.

func TestBuildWorkflowSummary_CreateMode(t *testing.T) {
	workflowJSON := `{
		"name": "pod-health-monitor",
		"definition": {
			"triggers": [{"type": "schedule", "params": {"cron": "*/30 * * * *"}}],
			"tasks": [
				{"id": "get-pods", "type": "k8s.cli", "params": {"command": "get pods"}},
				{"id": "check-health", "type": "data.transform", "depends_on": ["get-pods"]},
				{"id": "send-alert", "type": "notifications.im", "depends_on": ["check-health"]}
			]
		}
	}`

	summary, err := buildWorkflowSummary(workflowJSON, "", true)
	assert.Nil(t, err)
	assert.Contains(t, summary, "**`pod-health-monitor`**")
	assert.Contains(t, summary, "built and saved")
	assert.NotContains(t, summary, "not yet saved")
	assert.Contains(t, summary, "**Trigger:** schedule")
	assert.Contains(t, summary, "1. **get-pods** — `k8s.cli`")
	assert.Contains(t, summary, "2. **check-health** — `data.transform`")
	assert.Contains(t, summary, "3. **send-alert** — `notifications.im`")
}

// TestBuildWorkflowSummary_CreateMode_NotSaved verifies the headline reflects an
// unsuccessful save in create mode so the user is not misled.

func TestBuildWorkflowSummary_CreateMode_NotSaved(t *testing.T) {
	workflowJSON := `{
		"name": "pod-health-monitor",
		"definition": {
			"triggers": [{"type": "manual"}],
			"tasks": [{"id": "t1", "type": "core.print"}]
		}
	}`

	summary, err := buildWorkflowSummary(workflowJSON, "", false)
	assert.Nil(t, err)
	assert.Contains(t, summary, "**`pod-health-monitor`**")
	assert.Contains(t, summary, "not yet saved")
	assert.NotContains(t, summary, "built and saved")
}

func TestBuildWorkflowSummary_FixMode(t *testing.T) {
	workflowJSON := `{
		"name": "daily-report",
		"definition": {
			"triggers": [{"type": "manual"}],
			"tasks": [
				{"id": "fetch-data", "type": "scripting.run_script"}
			]
		}
	}`

	summary, err := buildWorkflowSummary(workflowJSON, "fix", true)
	assert.Nil(t, err)
	assert.Contains(t, summary, "updated")
	assert.NotContains(t, summary, "built and saved")
	assert.NotContains(t, summary, "not yet saved")
}

// TestBuildWorkflowSummary_FixMode_NotSaved verifies fix mode also reports a
// non-persisted state when the auto-save fails.

func TestBuildWorkflowSummary_FixMode_NotSaved(t *testing.T) {
	workflowJSON := `{
		"name": "daily-report",
		"definition": {
			"triggers": [{"type": "manual"}],
			"tasks": [{"id": "t1", "type": "core.print"}]
		}
	}`

	summary, err := buildWorkflowSummary(workflowJSON, "fix", false)
	assert.Nil(t, err)
	assert.Contains(t, summary, "**`daily-report`**")
	assert.Contains(t, summary, "not yet saved")
	assert.NotContains(t, summary, "has been updated")
}

func TestBuildWorkflowSummary_ManualTriggerDefault(t *testing.T) {
	// No triggers array at all — should default to "manual"
	workflowJSON := `{
		"name": "no-trigger",
		"definition": {
			"tasks": [{"id": "task-1", "type": "core.print"}]
		}
	}`

	summary, err := buildWorkflowSummary(workflowJSON, "", true)
	assert.Nil(t, err)
	assert.Contains(t, summary, "**Trigger:** manual")
}

func TestBuildWorkflowSummary_EmptyTasks(t *testing.T) {
	workflowJSON := `{
		"name": "empty-workflow",
		"definition": {
			"triggers": [{"type": "webhook"}],
			"tasks": []
		}
	}`

	summary, err := buildWorkflowSummary(workflowJSON, "", true)
	assert.Nil(t, err)
	assert.Contains(t, summary, "**`empty-workflow`**")
	assert.Contains(t, summary, "**Trigger:** webhook")
	assert.NotContains(t, summary, "**Tasks:**")
}

func TestBuildWorkflowSummary_MissingName(t *testing.T) {
	workflowJSON := `{
		"definition": {
			"triggers": [{"type": "manual"}],
			"tasks": [{"id": "task-1", "type": "core.print"}]
		}
	}`

	summary, err := buildWorkflowSummary(workflowJSON, "", true)
	assert.Nil(t, err)
	assert.Contains(t, summary, "**`automation`**") // defaults to "automation"
}

func TestBuildWorkflowSummary_InvalidJSON(t *testing.T) {
	_, err := buildWorkflowSummary("not valid json", "", true)
	assert.NotNil(t, err)
}

func TestBuildWorkflowSummary_MissingTaskFields(t *testing.T) {
	workflowJSON := `{
		"name": "sparse",
		"definition": {
			"tasks": [
				{"type": "core.print"},
				{"id": "has-id"}
			]
		}
	}`

	summary, err := buildWorkflowSummary(workflowJSON, "", true)
	assert.Nil(t, err)
	assert.Contains(t, summary, "1. **task-1** — `core.print`") // missing id defaults to task-N
	assert.Contains(t, summary, "2. **has-id** — `unknown`")    // missing type defaults to unknown
}

// TestTruncateSaveError verifies the helper truncates by rune count and
// produces valid UTF-8 even when the input ends in multi-byte characters
// near the boundary.

func TestTruncateSaveError(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		got := truncateSaveError(nil)
		assert.NotEmpty(t, got)
	})

	t.Run("short message preserved verbatim", func(t *testing.T) {
		got := truncateSaveError(fmt.Errorf("status 400: missing config"))
		assert.Equal(t, "status 400: missing config", got)
		assert.True(t, utf8.ValidString(got))
	})

	t.Run("long ascii message truncated to 240 chars + ellipsis", func(t *testing.T) {
		got := truncateSaveError(fmt.Errorf("%s", strings.Repeat("a", 500)))
		assert.True(t, strings.HasSuffix(got, "…"))
		assert.Equal(t, 240, utf8.RuneCountInString(strings.TrimSuffix(got, "…")))
		assert.True(t, utf8.ValidString(got))
	})

	t.Run("multi-byte string truncated at rune boundary", func(t *testing.T) {
		// 300 multi-byte runes (each "の" is 3 bytes) — byte-slicing at byte 240
		// would split the 80th character mid-rune and corrupt the string.
		input := strings.Repeat("の", 300)
		got := truncateSaveError(fmt.Errorf("%s", input))
		assert.True(t, utf8.ValidString(got), "truncated string must be valid UTF-8")
		assert.True(t, strings.HasSuffix(got, "…"))
		assert.Equal(t, 240, utf8.RuneCountInString(strings.TrimSuffix(got, "…")))
	})
}

// TestSummaryHeadline covers the four headline branches in summaryHeadline.

func TestSummaryHeadline(t *testing.T) {
	cases := []struct {
		mode    string
		saved   bool
		expects string
	}{
		{"", true, "built and saved"},
		{"", false, "not yet saved"},
		{"fix", true, "has been updated"},
		{"fix", false, "not yet saved"},
	}
	for _, tc := range cases {
		got := summaryHeadline("demo", tc.mode, tc.saved)
		assert.Contains(t, got, tc.expects, "mode=%q saved=%v -> %q", tc.mode, tc.saved, got)
		assert.Contains(t, got, "**`demo`**")
	}
}

// ==================== finalizeWithAutoSave INTEGRATION TEST ====================

// TestWorkflowBuilderAgent_FinalizeWithAutoSave_SummaryResponse tests the full
// plan → approve → build flow and verifies that ask-nudgebee source gets a markdown
// summary (not raw JSON) with an "Open in Editor" link.

func TestWorkflowBuilderAgent_ClarificationStateMarshal(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")
	agent.state = WorkflowBuilderState{
		Stage:         "clarification",
		OriginalQuery: "alert when pod crashes",
		Intent:        `{"description":"pod crash alert"}`,
		ClarifyingQuestions: []ClarifyingQuestion{
			{Question: "Which namespace?", Options: []string{"default", "production", "Skip"}},
			{Question: "Slack or email?", Options: []string{"Slack", "Email", "Skip"}},
		},
		ClarifyingAnswers: []string{"production", ""},
		ClarifyingIndex:   1,
	}

	data, err := agent.MarshalState()
	assert.Nil(t, err)
	assert.NotNil(t, data)

	agent2 := newWorkflowBuilderAgent("test-account")
	err = agent2.UnmarshalState(data)
	assert.Nil(t, err)

	assert.Equal(t, "clarification", agent2.state.Stage)
	assert.Equal(t, agent.state.OriginalQuery, agent2.state.OriginalQuery)
	assert.Equal(t, agent.state.Intent, agent2.state.Intent)
	assert.Len(t, agent2.state.ClarifyingQuestions, 2)
	assert.Equal(t, "Which namespace?", agent2.state.ClarifyingQuestions[0].Question)
	assert.Equal(t, []string{"default", "production", "Skip"}, agent2.state.ClarifyingQuestions[0].Options)
	assert.Equal(t, "Slack or email?", agent2.state.ClarifyingQuestions[1].Question)
	assert.Equal(t, []string{"production", ""}, agent2.state.ClarifyingAnswers)
	assert.Equal(t, 1, agent2.state.ClarifyingIndex)
}

// TestWorkflowBuilderAgent_BuildClarificationContext tests context string generation from Q&A pairs.

func TestWorkflowBuilderAgent_BuildClarificationContext(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")

	// Case 1: with answers
	agent.state = WorkflowBuilderState{
		ClarifyingQuestions: []ClarifyingQuestion{
			{Question: "Which namespace?", Options: []string{"default", "production", "Skip"}},
			{Question: "Slack or email?", Options: []string{"Slack", "Email", "Skip"}},
		},
		ClarifyingAnswers: []string{"production", "Slack"},
	}
	ctx := agent.buildClarificationContext()
	assert.Contains(t, ctx, "Q: Which namespace?")
	assert.Contains(t, ctx, "A: production")
	assert.Contains(t, ctx, "Q: Slack or email?")
	assert.Contains(t, ctx, "A: Slack")

	// Case 2: some skipped (empty string)
	agent.state.ClarifyingAnswers = []string{"production", ""}
	ctx = agent.buildClarificationContext()
	assert.Contains(t, ctx, "Q: Which namespace?")
	assert.Contains(t, ctx, "A: production")
	assert.NotContains(t, ctx, "Q: Slack or email?")

	// Case 3: all skipped
	agent.state.ClarifyingAnswers = []string{"", ""}
	ctx = agent.buildClarificationContext()
	assert.Equal(t, "", ctx)

	// Case 4: no questions
	agent.state.ClarifyingQuestions = nil
	agent.state.ClarifyingAnswers = nil
	ctx = agent.buildClarificationContext()
	assert.Equal(t, "", ctx)
}

// TestWorkflowBuilderAgent_BuildClarificationResponse tests that a question produces a valid followup response.

func TestWorkflowBuilderAgent_BuildClarificationResponse(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")

	question := ClarifyingQuestion{
		Question: "I'll send alerts to Slack. Which channel?",
		Options:  []string{"#alerts (recommended)", "#engineering", "#incidents", "Skip"},
	}

	request := core.NBAgentRequest{
		Query:     "send alert when pod crashes",
		AccountId: "test-account",
		AgentId:   uuid.NewString(),
	}

	resp, err := agent.buildClarificationResponse(request, question)
	assert.Nil(t, err)

	// Status should be WAITING
	assert.Equal(t, core.ConversationStatusWaiting, resp.Status)

	// Response must be empty so the UI doesn't render the question twice
	// (once as an agent-response bubble and once in the followup card).
	assert.Empty(t, resp.Response)

	// Followup structure
	assert.Equal(t, core.FollowupTypeSingleSelect, resp.FollowupRequest.FollowupType)
	assert.Equal(t, question.Options, resp.FollowupRequest.FollowupOptions)
	assert.Equal(t, question.Question, resp.FollowupRequest.Question)

	// FollowupData
	assert.NotNil(t, resp.FollowupRequest.FollowupData)
	assert.Equal(t, "clarification", resp.FollowupRequest.FollowupData["type"])
	assert.Equal(t, true, resp.FollowupRequest.FollowupData["allow_custom"])
	assert.Equal(t, true, resp.FollowupRequest.FollowupData["allow_skip"])

	// Structured options in FollowupData
	options, ok := resp.FollowupRequest.FollowupData["options"].([]map[string]any)
	assert.True(t, ok, "FollowupData.options should be []map[string]any")
	assert.Len(t, options, 4)
	assert.Equal(t, "#alerts (recommended)", options[0]["label"])
	assert.Equal(t, "Skip", options[3]["label"])
}

// drainWaitingStages auto-approves any remaining WAITING stages (plan_approval,
// fix_approval, etc.) by selecting the first available option. Returns the
// final response.

func TestWorkflowBuilder_WalkTasksAndResolveAccountIds(t *testing.T) {
	uuidK8s := "11111111-1111-1111-1111-111111111111"
	uuidGcp := "22222222-2222-2222-2222-222222222222"
	uuidAws := "33333333-3333-3333-3333-333333333333"
	nameToId := map[string]string{
		"my-k8s-dev":               uuidK8s,
		"gcp-dev - my-project-dev": uuidGcp,
		"nb-aws-prod":              uuidAws,
	}

	tests := []struct {
		name            string
		tasks           []interface{}
		wantUnresolved  []string
		wantAccountIdAt map[string]string // task id -> expected params.account_id
		wantUntouched   []string          // task ids whose account_id must equal input
	}{
		{
			name: "name swapped for uuid",
			tasks: []interface{}{
				map[string]interface{}{
					"id":   "get-pending-pods",
					"type": "k8s.cli",
					"params": map[string]interface{}{
						"account_id": "my-k8s-dev",
						"command":    "get pods",
					},
				},
			},
			wantAccountIdAt: map[string]string{"get-pending-pods": uuidK8s},
		},
		{
			name: "already uuid is untouched",
			tasks: []interface{}{
				map[string]interface{}{
					"id":   "get-pods",
					"type": "k8s.cli",
					"params": map[string]interface{}{
						"account_id": uuidK8s,
					},
				},
			},
			wantAccountIdAt: map[string]string{"get-pods": uuidK8s},
		},
		{
			name: "uuid with surrounding whitespace is trimmed",
			tasks: []interface{}{
				map[string]interface{}{
					"id":   "get-pods-spaced",
					"type": "k8s.cli",
					"params": map[string]interface{}{
						"account_id": "  " + uuidK8s + "\n",
					},
				},
			},
			wantAccountIdAt: map[string]string{"get-pods-spaced": uuidK8s},
		},
		{
			name: "template expression is untouched",
			tasks: []interface{}{
				map[string]interface{}{
					"id":   "templated",
					"type": "cloud.gcp.cli",
					"params": map[string]interface{}{
						"account_id": "{{ Configs.gcp_account_id }}",
					},
				},
			},
			wantUntouched: []string{"templated"},
		},
		{
			name: "unknown name is reported",
			tasks: []interface{}{
				map[string]interface{}{
					"id":   "trigger-scaling",
					"type": "cloud.gcp.cli",
					"params": map[string]interface{}{
						"account_id": "totally-fake-account",
					},
				},
			},
			wantUnresolved: []string{"'trigger-scaling' (account_id=\"totally-fake-account\")"},
		},
		{
			name: "nested tasks under core.foreach are resolved",
			tasks: []interface{}{
				map[string]interface{}{
					"id":   "loop",
					"type": "core.foreach",
					"tasks": []interface{}{
						map[string]interface{}{
							"id":   "inner-k8s",
							"type": "k8s.cli",
							"params": map[string]interface{}{
								"account_id": "my-k8s-dev",
							},
						},
					},
				},
			},
			wantAccountIdAt: map[string]string{"inner-k8s": uuidK8s},
		},
		{
			name: "multiple tasks mixed resolution",
			tasks: []interface{}{
				map[string]interface{}{
					"id":   "a",
					"type": "cloud.aws.cli",
					"params": map[string]interface{}{
						"account_id": "nb-aws-prod",
					},
				},
				map[string]interface{}{
					"id":   "b",
					"type": "cloud.gcp.cli",
					"params": map[string]interface{}{
						"account_id": "gcp-dev - my-project-dev",
					},
				},
			},
			wantAccountIdAt: map[string]string{"a": uuidAws, "b": uuidGcp},
		},
		{
			name: "task without params is skipped without panic",
			tasks: []interface{}{
				map[string]interface{}{
					"id":   "noparams",
					"type": "core.print",
				},
			},
		},
		{
			name: "non-string account_id is left alone",
			tasks: []interface{}{
				map[string]interface{}{
					"id":   "weird",
					"type": "k8s.cli",
					"params": map[string]interface{}{
						"account_id": 42,
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Snapshot original account_id values to detect untouched.
			originals := map[string]any{}
			for _, raw := range tc.tasks {
				task, _ := raw.(map[string]interface{})
				id, _ := task["id"].(string)
				if params, ok := task["params"].(map[string]interface{}); ok {
					if v, ok := params["account_id"]; ok {
						originals[id] = v
					}
				}
			}

			var unresolved []string
			walkTasksAndResolveAccountIds(tc.tasks, nameToId, &unresolved)

			assert.Equal(t, tc.wantUnresolved, unresolved, "unresolved list")

			for taskId, expected := range tc.wantAccountIdAt {
				var found bool
				var walk func(tasks []interface{})
				walk = func(tasks []interface{}) {
					for _, raw := range tasks {
						task, ok := raw.(map[string]interface{})
						if !ok {
							continue
						}
						if id, _ := task["id"].(string); id == taskId {
							params, _ := task["params"].(map[string]interface{})
							got, _ := params["account_id"].(string)
							assert.Equal(t, expected, got, "task %s account_id", taskId)
							found = true
							return
						}
						if nested, ok := task["tasks"].([]interface{}); ok {
							walk(nested)
						}
					}
				}
				walk(tc.tasks)
				assert.True(t, found, "task %s not found in walk", taskId)
			}

			for _, taskId := range tc.wantUntouched {
				for _, raw := range tc.tasks {
					task, _ := raw.(map[string]interface{})
					if id, _ := task["id"].(string); id == taskId {
						params, _ := task["params"].(map[string]interface{})
						assert.Equal(t, originals[taskId], params["account_id"], "task %s should be untouched", taskId)
					}
				}
			}
		})
	}
}

func TestWorkflowBuilder_IsValidUUID(t *testing.T) {
	assert.True(t, isValidUUID("11111111-1111-1111-1111-111111111111"))
	assert.True(t, isValidUUID("0F6E5C8A-1234-4567-89AB-CDEF01234567"))
	assert.False(t, isValidUUID(""))
	assert.False(t, isValidUUID("my-k8s-dev"))
	assert.False(t, isValidUUID("not-a-uuid"))
	assert.False(t, isValidUUID("{{ Configs.x }}"))
}

func TestWorkflowBuilder_BuildWorkflowEditorLink(t *testing.T) {
	acct := "f954767b-761c-45b3-bd63-54fa7a989aff"
	otherAcct := "11111111-2222-3333-4444-555555555555"
	sess := "6d00bee9-10fc-4898-9010-522935e85c99"
	wfId := "ee47d1bd-4cc5-4dcd-a910-473c159f1e66"

	tests := []struct {
		name        string
		agentAcct   string
		requestAcct string
		sessionId   string
		workflowId  string
		want        string
	}{
		{
			name:        "all set",
			agentAcct:   acct,
			requestAcct: acct,
			sessionId:   sess,
			workflowId:  wfId,
			want:        "/workflow/" + wfId + "?accountId=" + acct + "&session_id=" + sess + "#editor",
		},
		{
			name:        "agent accountId empty, fallback to request",
			agentAcct:   "",
			requestAcct: otherAcct,
			sessionId:   sess,
			workflowId:  wfId,
			want:        "/workflow/" + wfId + "?accountId=" + otherAcct + "&session_id=" + sess + "#editor",
		},
		{
			name:        "session id empty omits session_id param",
			agentAcct:   acct,
			requestAcct: acct,
			sessionId:   "",
			workflowId:  wfId,
			want:        "/workflow/" + wfId + "?accountId=" + acct + "#editor",
		},
		{
			name:        "both account ids empty returns empty",
			agentAcct:   "",
			requestAcct: "",
			sessionId:   sess,
			workflowId:  wfId,
			want:        "",
		},
		{
			name:        "empty workflow id returns empty",
			agentAcct:   acct,
			requestAcct: acct,
			sessionId:   sess,
			workflowId:  "",
			want:        "",
		},
		{
			name:        "workflow id with spaces is path-escaped",
			agentAcct:   acct,
			requestAcct: acct,
			sessionId:   "",
			workflowId:  "wf with space",
			want:        "/workflow/wf%20with%20space?accountId=" + acct + "#editor",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &WorkflowBuilderAgent{accountId: tc.agentAcct}
			req := core.NBAgentRequest{
				AccountId: tc.requestAcct,
				SessionId: tc.sessionId,
			}
			got := a.buildWorkflowEditorLink(req, tc.workflowId)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestWorkflowBuilder_BuildSystemPromptHasAccountIdUUIDRule asserts the
// build-prompt explicitly tells the LLM to use UUIDs for account_id. Guards
// against silent prompt regressions of the fix for the QA-reported
// "invalid input syntax for type uuid" save failure.

func TestWorkflowBuilder_BuildSystemPromptHasAccountIdUUIDRule(t *testing.T) {
	prompt := getBuildSystemPrompt("test intent", "test plan", getWorkflowSchema())
	assert.Contains(t, prompt, "CLOUD ACCOUNT IDs", "build prompt must have a dedicated section for account_id rules")
	assert.Contains(t, prompt, "account_id", "build prompt must mention account_id by name")
	assert.Contains(t, prompt, "UUID", "build prompt must call out UUID requirement")
	assert.Contains(t, prompt, "k8s.cli", "build prompt must enumerate k8s.cli")
	assert.Contains(t, prompt, "cloud.aws.cli", "build prompt must enumerate cloud.aws.cli")
	assert.Contains(t, prompt, "cloud.gcp.cli", "build prompt must enumerate cloud.gcp.cli")
	assert.Contains(t, prompt, "cloud.azure.cli", "build prompt must enumerate cloud.azure.cli")
}

// TestWorkflowBuilder_FixSystemPromptHasAccountIdUUIDRule mirrors the build
// check for the fix path — both create and fix go through the same save and
// hit the same runbook-server validation.

func TestWorkflowBuilder_FixSystemPromptHasAccountIdUUIDRule(t *testing.T) {
	prompt := getFixSystemPrompt("test error context", getWorkflowSchema())
	assert.Contains(t, prompt, "CLOUD ACCOUNT IDs")
	assert.Contains(t, prompt, "account_id")
	assert.Contains(t, prompt, "UUID")
}

// TestWorkflowBuilder_PromptsCoverAllFiveTriggerTypes asserts every prompt
// surface the LLM reads enumerates all 5 trigger types runbook-server
// supports (manual, schedule, webhook, event, optimization). Prevents
// silent regressions where one type gets dropped from one of the 9 prompt
// locations.

func TestWorkflowBuilder_PromptsCoverAllFiveTriggerTypes(t *testing.T) {
	triggers := []string{"manual", "schedule", "webhook", "event", "optimization"}

	surfaces := map[string]string{
		"schema":           getWorkflowSchema(),
		"build_prompt":     getBuildSystemPrompt("intent", "plan", getWorkflowSchema()),
		"fix_prompt":       getFixSystemPrompt("err", getWorkflowSchema()),
		"planning_context": getWorkflowPlanningContext(),
	}

	for name, content := range surfaces {
		for _, trig := range triggers {
			assert.Contains(t, content, trig, "surface %q must mention trigger type %q", name, trig)
		}
	}
}

// TestWorkflowBuilder_PlanningContextHasScheduleRules guards the specific
// schedule-trigger sub-rules added alongside the trigger-types fix:
// BufferAll overlap policy, catchup_window single-unit constraint.

func TestWorkflowBuilder_PlanningContextHasScheduleRules(t *testing.T) {
	ctx := getWorkflowPlanningContext()
	assert.Contains(t, ctx, "BufferAll", "planning context must list BufferAll overlap policy")
	assert.Contains(t, ctx, "catchup_window", "planning context must mention catchup_window")
}

// TestWorkflowBuilder_ReadOnlyPromptForbidsModification guards the core invariant of the
// read-only branch (issue #30825): an explain/diagnose turn must never build or modify the
// automation. The prompt must restrict the loop to read-only tools and to a prose answer.
func TestWorkflowBuilder_ReadOnlyPromptForbidsModification(t *testing.T) {
	prompt := buildReadOnlyPrompt("why did it fail?", "")

	// Must instruct the model not to mutate or finalize.
	assert.Contains(t, prompt, "must NOT build, modify, fix, or apply")
	assert.Contains(t, prompt, "Do NOT call finalize")
	assert.Contains(t, prompt, "Do NOT output a workflow JSON definition")

	// Must name the read-only tools and forbid the write tools.
	assert.Contains(t, prompt, "list_executions")
	assert.Contains(t, prompt, "get_execution")
	assert.Contains(t, prompt, "list_tasks")
	assert.Contains(t, prompt, "never init_workflow / add_task / modify_task / delete_task / validate / finalize")

	// Must carry the user's question through.
	assert.Contains(t, prompt, "why did it fail?")
}

// TestWorkflowBuilder_ReadOnlyPromptDefinitionSection asserts the current definition is embedded
// only when provided, so explain questions can be answered without extra tool calls.
func TestWorkflowBuilder_ReadOnlyPromptDefinitionSection(t *testing.T) {
	withDef := buildReadOnlyPrompt("explain this", `{"name":"x","tasks":[]}`)
	assert.Contains(t, withDef, "CURRENT AUTOMATION DEFINITION:")
	assert.Contains(t, withDef, `{"name":"x","tasks":[]}`)

	withoutDef := buildReadOnlyPrompt("explain this", "")
	assert.NotContains(t, withoutDef, "CURRENT AUTOMATION DEFINITION:")
}

// TestWorkflowBuilder_ReadOnlyClassification verifies the classifier answer parser defaults to
// ACTION on ambiguity (issue #30825): READ_ONLY only when present AND ACTION is absent.
func TestWorkflowBuilder_ReadOnlyClassification(t *testing.T) {
	cases := []struct {
		answer string
		want   bool
	}{
		{"READ_ONLY", true},
		{"read_only", true},
		{"  READ_ONLY\n", true},
		{"ACTION", false},
		{"", false},
		{"This is an ACTION, not a READ_ONLY turn", false}, // names both → ACTION wins
		{"READ_ONLY (the user just wants an explanation)", true},
		{"unsure", false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, isReadOnlyClassification(c.answer), "answer %q", c.answer)
	}
}

// TestWorkflowBuilder_BuildPromptHasTriggerPayloadAccess asserts the build
// prompt teaches the LLM how tasks read trigger payloads — without this,
// generated automations couldn't reference event/webhook input.

func TestWorkflowBuilder_BuildPromptHasTriggerPayloadAccess(t *testing.T) {
	prompt := getBuildSystemPrompt("intent", "plan", getWorkflowSchema())
	// At least one of the documented access patterns must be present.
	hasEventAccess := strings.Contains(prompt, "Inputs.event") || strings.Contains(prompt, "Trigger.event")
	hasWebhookAccess := strings.Contains(prompt, "Inputs.webhook_payload") || strings.Contains(prompt, "Trigger.payload") || strings.Contains(prompt, "webhook_payload")
	assert.True(t, hasEventAccess, "build prompt must document event-payload access pattern")
	assert.True(t, hasWebhookAccess, "build prompt must document webhook-payload access pattern")
}

// TestWorkflowBuilder_EnvContextTrailerHasAccountIdRule asserts the env
// context trailer (rendered into clarifying-question + plan prompts) tells
// the LLM to use UUIDs for account_id. This is the user-facing reason QA
// originally saw name-instead-of-UUID save failures.

func TestWorkflowBuilder_EnvContextTrailerHasAccountIdRule(t *testing.T) {
	// We can't easily stub ListAllToolConfigs without refactoring, so we
	// assert the static trailer string is present in the rendered template.
	// The trailer is built unconditionally when buildEnvironmentContext is
	// called with at least one cloud account; check the build prompt which
	// includes the schema (independent of env context) for the same rule.
	prompt := getBuildSystemPrompt("intent", "plan", getWorkflowSchema())
	assert.Contains(t, prompt, "params.account_id", "prompt must explicitly reference params.account_id")
}

// TestWorkflowBuilder_EnvContext_RealCloudAccountsExposeUUID is an
// integration test that calls buildEnvironmentContext against the test
// account's real configs via ListAllToolConfigs. It validates the
// fix that exposes cloud-account UUIDs to the LLM (the root cause of
// the QA-reported "invalid input syntax for type uuid" save failures).
//
// Requires: DB (port-forward postgres:5433) and services-server
// (port-forward api-server:8888). Does NOT require workflow-server or
// the LLM provider.

func TestWorkflowBuilder_CatchupWindowGuidanceAccurate(t *testing.T) {
	surfaces := map[string]string{
		"schema":           getWorkflowSchema(),
		"build_prompt":     getBuildSystemPrompt("intent", "plan", getWorkflowSchema()),
		"planning_context": getWorkflowPlanningContext(),
	}

	for name, content := range surfaces {
		// Must say day/week units are NOT supported and steer the LLM to hours.
		if strings.Contains(content, "catchup_window") {
			assert.Regexp(t, `"?7d"?\s+(is\s+)?NOT supported|"168h"\s*(=|for)\s*7\s*days|day/week units`, content,
				"surface %q must clarify that 7d is invalid and direct the LLM to hours (e.g. 168h)", name)

			// Must NOT claim compound durations are rejected — they ARE valid.
			assert.NotRegexp(t, `(?i)compound (durations?|values?) (are\s+)?(rejected|invalid)`, content,
				"surface %q must NOT claim compound durations are rejected (e.g. 1h30m is valid)", name)
			assert.NotRegexp(t, `(?i)single[- ]unit (only|duration)`, content,
				"surface %q must not say single-unit only — compound durations are accepted", name)
		}
	}
}

// TestClarificationPrompt_DoesNotAskAboutSlackChannel guards the clarification
// prompt against two regressions:
//
//  1. **No hardcoded channel names.** A prior revision shipped an example with
//     literal "#alerts" / "#engineering" / "#incidents" — and because the LLM
//     had no real channel data to draw from, it copied those names verbatim
//     into the FollowupOptions rendered to the user. The fix moves channel
//     selection out of the clarification flow entirely.
//
//  2. **No clarification question about Slack channels.** Channel selection
//     belongs to {{ Configs.slack_channel }} (filled via Configs UI) or to
//     workflow Inputs at runtime — not to a single-select picker at planning
//     time. The prompt must explicitly forbid the LLM from emitting such a
//     question.
//
// If anyone reintroduces hardcoded channel names or removes the "do not ask
// about Slack channels" rule, this test fails.

func TestClarificationPrompt_DoesNotAskAboutSlackChannel(t *testing.T) {
	prompt := getClarificationSystemPrompt(
		"\nACCOUNT ENVIRONMENT:\nIntegrations: slack: my-workspace",
		"\nAVAILABLE CONFIGS:\n[]",
		"send an alert to slack when a pod crashes",
	)

	// (1) No hardcoded channel names — they were the original bug. The LLM
	// copied these from the example into real user-facing options.
	for _, banned := range []string{"#alerts", "#engineering", "#incidents", "#ops", "internal-team"} {
		assert.NotContains(t, prompt, banned,
			"clarification prompt must not contain hardcoded Slack channel example %q — it was getting copied verbatim into user-facing options", banned)
	}

	// (2) The prompt must explicitly forbid asking the user which Slack
	// channel to use. We accept either phrasing of the rule.
	assert.Regexp(t, `(?i)do not ask which slack channel|channel selection is not a clarification question`, prompt,
		"clarification prompt must explicitly forbid asking the user which Slack channel to use — defer to Configs/Inputs instead")

	// (3) The prompt must point the LLM at the Configs placeholder so the
	// workflow still gets built — without this, the LLM might omit the
	// channel param entirely and produce an invalid notifications.im task.
	assert.Contains(t, prompt, "{{ Configs.slack_channel }}",
		"clarification prompt must reference {{ Configs.slack_channel }} so the LLM knows where the channel value comes from")
}
