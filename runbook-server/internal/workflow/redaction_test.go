package workflow

import (
	"nudgebee/runbook/internal/model"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContainsSecretReference(t *testing.T) {
	tests := []struct {
		name     string
		value    any
		expected bool
	}{
		{"jinja bracket syntax", "{{ Secrets['api_key'] }}", true},
		{"jinja dot syntax", "{{ Secrets.token }}", true},
		{"jinja double quote bracket", `{{ Secrets["pw"] }}`, true},
		{"embedded in string", "Bearer {{ Secrets['token'] }}", true},
		{"go template syntax", "{{.Secrets.key}}", true},
		{"inputs reference", "{{ Inputs.name }}", false},
		{"configs reference", "{{ Configs['key'] }}", false},
		{"empty string", "", false},
		{"nil value", nil, false},
		{"nested map with secret", map[string]any{"a": "{{ Secrets.x }}"}, true},
		{"nested map without secret", map[string]any{"a": "hello"}, false},
		{"slice with secret", []any{"a", "{{ Secrets.x }}"}, true},
		{"slice without secret", []any{"a", "b"}, false},
		{"deeply nested map", map[string]any{
			"config": map[string]any{
				"auth": "{{ Secrets['token'] }}",
			},
		}, true},
		{"integer value", 42, false},
		{"boolean value", true, false},
		{"float value", 3.14, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, containsSecretReference(tt.value))
		})
	}
}

func TestBuildTaskDefinitionMap(t *testing.T) {
	tasks := []model.Task{
		{ID: "task1", Type: "core.print", Params: map[string]any{"message": "hello"}},
		{ID: "group1", Type: "core.group", Tasks: []model.Task{
			{ID: "subtask1", Type: "core.print", Params: map[string]any{"message": "{{ Secrets.key }}"}},
			{ID: "subtask2", Type: "http.request"},
		}},
		{ID: "task2", Type: "core.print"},
	}

	m := buildTaskDefinitionMap(tasks)

	assert.Len(t, m, 5)
	assert.NotNil(t, m["task1"])
	assert.NotNil(t, m["group1"])
	assert.NotNil(t, m["subtask1"])
	assert.NotNil(t, m["subtask2"])
	assert.NotNil(t, m["task2"])
	assert.Equal(t, "core.print", m["subtask1"].Type)
}

func TestBuildTaskDefinitionMap_Empty(t *testing.T) {
	m := buildTaskDefinitionMap(nil)
	assert.Empty(t, m)
}

func TestBuildSecretParamKeys(t *testing.T) {
	taskDefs := map[string]*model.Task{
		"t1": {ID: "t1", Params: map[string]any{
			"url":   "https://api.example.com",
			"token": "{{ Secrets['api_token'] }}",
			"auth":  "Bearer {{ Secrets.key }}",
		}},
		"t2": {ID: "t2", Params: map[string]any{
			"message": "hello",
		}},
		"t3": {ID: "t3", Params: nil},
	}

	result := buildSecretParamKeys(taskDefs)

	assert.Contains(t, result, "t1")
	assert.True(t, result["t1"]["token"])
	assert.True(t, result["t1"]["auth"])
	assert.False(t, result["t1"]["url"])
	assert.NotContains(t, result, "t2")
	assert.NotContains(t, result, "t3")
}

func TestRedactTaskInput(t *testing.T) {
	t.Run("basic redaction", func(t *testing.T) {
		input := map[string]any{
			"url":   "https://api.example.com",
			"token": "actual-secret-value",
		}
		secretKeys := map[string]bool{"token": true}

		redacted := redactTaskInput(input, secretKeys)

		assert.Equal(t, "https://api.example.com", redacted["url"])
		assert.Equal(t, RedactedValue, redacted["token"])
	})

	t.Run("nil input", func(t *testing.T) {
		result := redactTaskInput(nil, map[string]bool{"token": true})
		assert.Nil(t, result)
	})

	t.Run("empty secret keys", func(t *testing.T) {
		input := map[string]any{"token": "secret"}
		result := redactTaskInput(input, nil)
		assert.Equal(t, "secret", result["token"])
	})

	t.Run("does not modify original", func(t *testing.T) {
		input := map[string]any{"token": "secret", "url": "http://example.com"}
		secretKeys := map[string]bool{"token": true}

		redacted := redactTaskInput(input, secretKeys)

		assert.Equal(t, "secret", input["token"]) // original unchanged
		assert.Equal(t, RedactedValue, redacted["token"])
	})
}

func TestResolveSecretKeysForTask(t *testing.T) {
	secretParamKeys := map[string]map[string]bool{
		"api_call": {"token": true},
		"db_query": {"password": true},
	}

	t.Run("direct match", func(t *testing.T) {
		keys := resolveSecretKeysForTask("api_call", secretParamKeys)
		assert.NotNil(t, keys)
		assert.True(t, keys["token"])
	})

	t.Run("foreach prefix match", func(t *testing.T) {
		keys := resolveSecretKeysForTask("loop-2-api_call", secretParamKeys)
		assert.NotNil(t, keys)
		assert.True(t, keys["token"])
	})

	t.Run("no match", func(t *testing.T) {
		keys := resolveSecretKeysForTask("unknown_task", secretParamKeys)
		assert.Nil(t, keys)
	})

	t.Run("partial name no match", func(t *testing.T) {
		keys := resolveSecretKeysForTask("call", secretParamKeys)
		assert.Nil(t, keys)
	})
}

func TestRedactSecretsFromTasks_BasicCase(t *testing.T) {
	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "fetch", Type: "http.request", Params: map[string]any{
				"url":           "https://api.example.com",
				"authorization": "Bearer {{ Secrets['token'] }}",
			}},
			{ID: "print", Type: "core.print", Params: map[string]any{
				"message": "{{ Tasks.fetch.output }}",
			}},
		},
	}
	tasks := []model.TaskExecutionDetails{
		{ID: "fetch", Input: map[string]any{
			"url":           "https://api.example.com",
			"authorization": "Bearer sk-1234567890abcdef",
			"__tenant_id":   "t1",
		}},
		{ID: "print", Input: map[string]any{
			"message": "OK",
		}},
	}

	RedactSecretsFromTasks(tasks, wfDef)

	assert.Equal(t, RedactedValue, tasks[0].Input["authorization"])
	assert.Equal(t, "https://api.example.com", tasks[0].Input["url"])
	assert.Equal(t, "t1", tasks[0].Input["__tenant_id"])
	assert.Equal(t, "OK", tasks[1].Input["message"])
}

func TestRedactSecretsFromTasks_NestedParamDefinition(t *testing.T) {
	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "call_api", Type: "http.request", Params: map[string]any{
				"headers": map[string]any{
					"Authorization": "{{ Secrets['token'] }}",
				},
				"url": "https://example.com",
			}},
		},
	}
	tasks := []model.TaskExecutionDetails{
		{ID: "call_api", Input: map[string]any{
			"headers": map[string]any{"Authorization": "Bearer real-secret"},
			"url":     "https://example.com",
		}},
	}

	RedactSecretsFromTasks(tasks, wfDef)

	// "headers" key in definition contains nested secret ref → entire field redacted
	assert.Equal(t, RedactedValue, tasks[0].Input["headers"])
	assert.Equal(t, "https://example.com", tasks[0].Input["url"])
}

func TestRedactSecretsFromTasks_Children(t *testing.T) {
	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "loop", Type: "core.foreach", Tasks: []model.Task{
				{ID: "api_call", Type: "http.request", Params: map[string]any{
					"token": "{{ Secrets.key }}",
					"url":   "{{ Vars.LoopItem.url }}",
				}},
			}},
		},
	}
	tasks := []model.TaskExecutionDetails{
		{ID: "loop", Children: []model.TaskExecutionDetails{
			{ID: "Iteration 0", Children: []model.TaskExecutionDetails{
				{ID: "api_call", Input: map[string]any{
					"token": "secret-value",
					"url":   "https://example.com",
				}},
			}},
		}},
	}

	RedactSecretsFromTasks(tasks, wfDef)

	child := tasks[0].Children[0].Children[0]
	assert.Equal(t, RedactedValue, child.Input["token"])
	assert.Equal(t, "https://example.com", child.Input["url"])
}

func TestRedactSecretsFromTasks_NoSecretsInDefinition(t *testing.T) {
	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "print", Type: "core.print", Params: map[string]any{
				"message": "{{ Inputs.name }}",
			}},
		},
	}
	tasks := []model.TaskExecutionDetails{
		{ID: "print", Input: map[string]any{
			"message": "Hello World",
		}},
	}

	RedactSecretsFromTasks(tasks, wfDef)

	assert.Equal(t, "Hello World", tasks[0].Input["message"])
}

func TestRedactSecretsFromTasks_NilInput(t *testing.T) {
	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "t1", Type: "core.print", Params: map[string]any{
				"message": "{{ Secrets.key }}",
			}},
		},
	}
	tasks := []model.TaskExecutionDetails{
		{ID: "t1", Input: nil},
	}

	// Should not panic
	RedactSecretsFromTasks(tasks, wfDef)
	assert.Nil(t, tasks[0].Input)
}

func TestRedactSecretsFromTasks_EmptyTasks(t *testing.T) {
	wfDef := model.WorkflowDefinition{Tasks: nil}
	tasks := []model.TaskExecutionDetails{}

	// Should not panic
	RedactSecretsFromTasks(tasks, wfDef)
}

func TestRedactSecretsFromTasks_MultipleSecretKeys(t *testing.T) {
	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "t1", Type: "http.request", Params: map[string]any{
				"api_key":  "{{ Secrets['key1'] }}",
				"password": "{{ Secrets['key2'] }}",
				"url":      "https://example.com",
			}},
		},
	}
	tasks := []model.TaskExecutionDetails{
		{ID: "t1", Input: map[string]any{
			"api_key":  "secret1",
			"password": "secret2",
			"url":      "https://example.com",
		}},
	}

	RedactSecretsFromTasks(tasks, wfDef)

	assert.Equal(t, RedactedValue, tasks[0].Input["api_key"])
	assert.Equal(t, RedactedValue, tasks[0].Input["password"])
	assert.Equal(t, "https://example.com", tasks[0].Input["url"])
}

func TestRedactSecretsFromTasks_ForeachPrefixedTaskID(t *testing.T) {
	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "loop", Type: "core.foreach", Tasks: []model.Task{
				{ID: "call", Type: "http.request", Params: map[string]any{
					"token": "{{ Secrets.api_token }}",
				}},
			}},
		},
	}
	// Foreach creates prefixed task IDs in Temporal history
	tasks := []model.TaskExecutionDetails{
		{ID: "loop-0-call", Input: map[string]any{
			"token": "real-secret",
			"url":   "https://example.com",
		}},
		{ID: "loop-1-call", Input: map[string]any{
			"token": "real-secret-2",
			"url":   "https://example2.com",
		}},
	}

	RedactSecretsFromTasks(tasks, wfDef)

	assert.Equal(t, RedactedValue, tasks[0].Input["token"])
	assert.Equal(t, "https://example.com", tasks[0].Input["url"])
	assert.Equal(t, RedactedValue, tasks[1].Input["token"])
	assert.Equal(t, "https://example2.com", tasks[1].Input["url"])
}
