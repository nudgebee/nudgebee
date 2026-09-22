package workflow

import (
	"nudgebee/runbook/internal/model"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRedactSecretsFromTasks_RedactsRenderedParams is the motivating regression:
// the executions panel renders `rendered_params ?? input`, so redacting only
// Input left every modern task showing resolved secrets in the clear.
func TestRedactSecretsFromTasks_RedactsRenderedParams(t *testing.T) {
	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "fetch", Type: "http.request", Params: map[string]any{
				"url":           "https://api.example.com",
				"authorization": "Bearer {{ Secrets['token'] }}",
			}},
		},
	}
	tasks := []model.TaskExecutionDetails{
		{
			ID: "fetch",
			Input: map[string]any{
				"url":           "https://api.example.com",
				"authorization": "Bearer sk-1234567890abcdef",
			},
			RenderedParams: map[string]any{
				"url":           "https://api.example.com",
				"authorization": "Bearer sk-1234567890abcdef",
			},
		},
	}

	RedactSecretsFromTasks(tasks, wfDef)

	assert.Equal(t, RedactedValue, tasks[0].RenderedParams["authorization"])
	assert.Equal(t, "https://api.example.com", tasks[0].RenderedParams["url"])
	assert.Equal(t, RedactedValue, tasks[0].Input["authorization"])
}

// TestRedactSecretsFromTasks_RedactsRenderedParamsWithoutInput covers tasks
// that carry rendered_params but no input, so the Input nil-guard cannot be
// the thing that drives redaction.
func TestRedactSecretsFromTasks_RedactsRenderedParamsWithoutInput(t *testing.T) {
	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "fetch", Type: "http.request", Params: map[string]any{
				"authorization": "Bearer {{ Secrets['token'] }}",
			}},
		},
	}
	tasks := []model.TaskExecutionDetails{
		{ID: "fetch", RenderedParams: map[string]any{"authorization": "Bearer sk-live-abcdef"}},
	}

	RedactSecretsFromTasks(tasks, wfDef)

	assert.Equal(t, RedactedValue, tasks[0].RenderedParams["authorization"])
}

// TestRedactSecretsFromTasks_NestedSecretRedactsOnlyTheLeaf verifies the
// definition walk collects secret-referencing key names at any depth, so a
// sibling under the same parent map survives.
func TestRedactSecretsFromTasks_NestedSecretRedactsOnlyTheLeaf(t *testing.T) {
	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "call_api", Type: "http.request", Params: map[string]any{
				"headers": map[string]any{
					"Authorization": "Bearer {{ Secrets['token'] }}",
					"Accept":        "application/json",
				},
				"url": "https://example.com",
			}},
		},
	}
	tasks := []model.TaskExecutionDetails{
		{ID: "call_api", Input: map[string]any{
			"headers": map[string]any{
				"Authorization": "Bearer sk-live-abcdef",
				"Accept":        "application/json",
			},
			"url": "https://example.com",
		}},
	}

	RedactSecretsFromTasks(tasks, wfDef)

	headers, ok := tasks[0].Input["headers"].(map[string]any)
	require.True(t, ok, "headers must stay a map, not collapse to a placeholder")
	assert.Equal(t, RedactedValue, headers["Authorization"])
	assert.Equal(t, "application/json", headers["Accept"])
	assert.Equal(t, "https://example.com", tasks[0].Input["url"])
}

// TestRedactSecretsFromTasks_SecretInsideSlice covers the deep walk descending
// through slices, not only maps.
func TestRedactSecretsFromTasks_SecretInsideSlice(t *testing.T) {
	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "post", Type: "http.request", Params: map[string]any{
				"headers": []any{
					map[string]any{"name": "Accept", "value": "application/json"},
					map[string]any{"name": "Authorization", "token": "{{ Secrets['token'] }}"},
				},
			}},
		},
	}
	tasks := []model.TaskExecutionDetails{
		{ID: "post", Input: map[string]any{
			"headers": []any{
				map[string]any{"name": "Accept", "value": "application/json"},
				map[string]any{"name": "Authorization", "token": "sk-live-abcdef"},
			},
		}},
	}

	RedactSecretsFromTasks(tasks, wfDef)

	headers, ok := tasks[0].Input["headers"].([]any)
	require.True(t, ok, "headers must stay a slice")
	require.Len(t, headers, 2)
	first, ok := headers[0].(map[string]any)
	require.True(t, ok)
	second, ok := headers[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "application/json", first["value"])
	assert.Equal(t, RedactedValue, second["token"])
	assert.Equal(t, "Authorization", second["name"])
}

// TestRedactSecretsFromTasks_RawWrapper covers executor.go:1305, which wraps a
// non-map rendered param set as {"_raw": ...}. Key names are collected by name
// rather than by path from the definition root precisely so this still matches.
func TestRedactSecretsFromTasks_RawWrapper(t *testing.T) {
	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "run", Type: "core.script", Params: map[string]any{
				"_raw": "echo {{ Secrets['db_password'] }}",
			}},
		},
	}
	tasks := []model.TaskExecutionDetails{
		{ID: "run", Input: map[string]any{"_raw": "echo Zx9-canary-7Qw"}},
	}

	RedactSecretsFromTasks(tasks, wfDef)

	assert.Equal(t, RedactedValue, tasks[0].Input["_raw"])
}

// TestRedactSecretsFromTasks_DoesNotMutateDefinition is the highest-risk
// property in this change. service.go:4899 and :5000 build synthesized and
// skipped tasks whose Input aliases wfDef.Tasks[].Params directly, so an
// in-place redact would blank the template text on the canvas and corrupt the
// definition RetriggerWorkflowExecution replays from.
func TestRedactSecretsFromTasks_DoesNotMutateDefinition(t *testing.T) {
	nestedDefParams := map[string]any{
		"Authorization": "Bearer {{ Secrets['token'] }}",
		"Accept":        "application/json",
	}
	defParams := map[string]any{
		"headers": nestedDefParams,
		"url":     "https://example.com",
	}
	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{{ID: "call_api", Type: "http.request", Params: defParams}},
	}
	// Mirrors service.go:5000 — a skipped branch surfaces the definition params
	// as its Input, aliasing the very map the canvas renders from.
	tasks := []model.TaskExecutionDetails{
		{ID: "call_api", Status: model.TaskStatusSkipped, Input: defParams},
	}

	RedactSecretsFromTasks(tasks, wfDef)

	assert.Equal(t, "Bearer {{ Secrets['token'] }}", nestedDefParams["Authorization"],
		"definition template text must survive redaction")
	assert.Equal(t, "application/json", nestedDefParams["Accept"])
	assert.Equal(t, "https://example.com", defParams["url"])
	headers, ok := defParams["headers"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Bearer {{ Secrets['token'] }}", headers["Authorization"])
}

// TestRedactSecretsFromTasks_DoesNotLeakThroughSharedNestedMaps covers the
// aliasing service.go:4264-4278 creates: RenderedParams is a fresh top-level
// map but its values are the same references Input holds. Redacting one must
// not depend on, or be undone by, redacting the other.
func TestRedactSecretsFromTasks_DoesNotLeakThroughSharedNestedMaps(t *testing.T) {
	sharedHeaders := map[string]any{"Authorization": "Bearer sk-live-abcdef"}
	input := map[string]any{"headers": sharedHeaders, "__tenant_id": "t1"}
	rendered := map[string]any{"headers": sharedHeaders}

	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "call_api", Type: "http.request", Params: map[string]any{
				"headers": map[string]any{"Authorization": "Bearer {{ Secrets['token'] }}"},
			}},
		},
	}
	tasks := []model.TaskExecutionDetails{
		{ID: "call_api", Input: input, RenderedParams: rendered},
	}

	RedactSecretsFromTasks(tasks, wfDef)

	inputHeaders, ok := tasks[0].Input["headers"].(map[string]any)
	require.True(t, ok, "input headers must stay a map")
	renderedHeaders, ok := tasks[0].RenderedParams["headers"].(map[string]any)
	require.True(t, ok, "rendered_params headers must stay a map")
	assert.Equal(t, RedactedValue, inputHeaders["Authorization"])
	assert.Equal(t, RedactedValue, renderedHeaders["Authorization"])
	assert.Equal(t, "t1", tasks[0].Input["__tenant_id"])
}

// TestRedactSecretsFromTasks_ChildrenRenderedParams confirms the recursion into
// group/foreach children covers RenderedParams too.
func TestRedactSecretsFromTasks_ChildrenRenderedParams(t *testing.T) {
	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "group", Type: "core.group", Tasks: []model.Task{
				{ID: "inner", Type: "http.request", Params: map[string]any{
					"authorization": "{{ Secrets['token'] }}",
				}},
			}},
		},
	}
	tasks := []model.TaskExecutionDetails{
		{ID: "group", Children: []model.TaskExecutionDetails{
			{ID: "inner", RenderedParams: map[string]any{"authorization": "sk-live-abcdef"}},
		}},
	}

	RedactSecretsFromTasks(tasks, wfDef)

	assert.Equal(t, RedactedValue, tasks[0].Children[0].RenderedParams["authorization"])
}

// TestRedactSecretsFromTasks_NonSecretTaskUntouched guards against the deep
// walk over-reaching into tasks whose definition references no secret.
func TestRedactSecretsFromTasks_NonSecretTaskUntouched(t *testing.T) {
	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "a", Type: "http.request", Params: map[string]any{
				"authorization": "Bearer {{ Secrets['token'] }}",
			}},
			{ID: "b", Type: "core.print", Params: map[string]any{
				"message":       "hello",
				"authorization": "static-not-a-secret",
			}},
		},
	}
	tasks := []model.TaskExecutionDetails{
		{ID: "b", Input: map[string]any{
			"message":       "hello",
			"authorization": "static-not-a-secret",
		}},
	}

	RedactSecretsFromTasks(tasks, wfDef)

	assert.Equal(t, "static-not-a-secret", tasks[0].Input["authorization"],
		"secret key names must not leak across task definitions")
	assert.Equal(t, "hello", tasks[0].Input["message"])
}

// TestRedactSecretsFromTasks_BareSecretInSlice covers a Secrets reference held
// directly as a list element — shell-style `args`, a list of tokens — where
// there is no map key of its own for the leaf walk to name. The whole
// containing key must blank, which is what the pre-deep redactor did.
func TestRedactSecretsFromTasks_BareSecretInSlice(t *testing.T) {
	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "run", Type: "core.script", Params: map[string]any{
				"command": "curl",
				"args":    []any{"--token", "{{ Secrets['db_password'] }}"},
			}},
		},
	}
	tasks := []model.TaskExecutionDetails{
		{ID: "run", Input: map[string]any{
			"command": "curl",
			"args":    []any{"--token", "Zx9-canary-7Qw"},
		}},
	}

	RedactSecretsFromTasks(tasks, wfDef)

	assert.Equal(t, RedactedValue, tasks[0].Input["args"])
	assert.Equal(t, "curl", tasks[0].Input["command"])
}

// TestRedactSecretsFromTasks_BareSecretInNestedSlice covers the same shape one
// slice deeper, which a non-recursive guard would miss.
func TestRedactSecretsFromTasks_BareSecretInNestedSlice(t *testing.T) {
	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "run", Type: "core.script", Params: map[string]any{
				"arg_groups": []any{
					[]any{"--verbose"},
					[]any{"--token", "{{ Secrets['db_password'] }}"},
				},
			}},
		},
	}
	tasks := []model.TaskExecutionDetails{
		{ID: "run", Input: map[string]any{
			"arg_groups": []any{
				[]any{"--verbose"},
				[]any{"--token", "Zx9-canary-7Qw"},
			},
		}},
	}

	RedactSecretsFromTasks(tasks, wfDef)

	assert.Equal(t, RedactedValue, tasks[0].Input["arg_groups"])
}

// TestRedactSecretsFromTasks_SliceOfMapsStaysLeafPrecise guards the fix above
// from over-reaching: when the slice holds maps, their keys are nameable, so
// only the secret leaf blanks and the list keeps its shape.
func TestRedactSecretsFromTasks_SliceOfMapsStaysLeafPrecise(t *testing.T) {
	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "post", Type: "http.request", Params: map[string]any{
				"headers": []any{
					map[string]any{"name": "Accept", "value": "application/json"},
					map[string]any{"name": "Authorization", "token": "{{ Secrets['token'] }}"},
				},
			}},
		},
	}
	tasks := []model.TaskExecutionDetails{
		{ID: "post", Input: map[string]any{
			"headers": []any{
				map[string]any{"name": "Accept", "value": "application/json"},
				map[string]any{"name": "Authorization", "token": "sk-live-abcdef"},
			},
		}},
	}

	RedactSecretsFromTasks(tasks, wfDef)

	headers, ok := tasks[0].Input["headers"].([]any)
	require.True(t, ok, "a slice of maps must keep its shape, not collapse")
	require.Len(t, headers, 2)
	second, ok := headers[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, RedactedValue, second["token"])
	assert.Equal(t, "Authorization", second["name"])
}

// TestRedactSecretsFromTasks_SecretAsMapKey covers a Secrets reference used as
// a map KEY rather than a value — naming an env var or header from a secret.
// The resolved key name is the secret itself, and there is nowhere to put a
// placeholder, so the containing key blanks. Same class as a bare slice
// element: a secret in a position no key name can address.
func TestRedactSecretsFromTasks_SecretAsMapKey(t *testing.T) {
	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "run", Type: "core.script", Params: map[string]any{
				"command": "printenv",
				"env": map[string]any{
					"{{ Secrets['db_password'] }}": "static-value",
				},
			}},
		},
	}
	tasks := []model.TaskExecutionDetails{
		{ID: "run", Input: map[string]any{
			"command": "printenv",
			"env":     map[string]any{"Zx9-canary-7Qw": "static-value"},
		}},
	}

	RedactSecretsFromTasks(tasks, wfDef)

	assert.Equal(t, RedactedValue, tasks[0].Input["env"])
	assert.Equal(t, "printenv", tasks[0].Input["command"])
}

// TestRedactSecretsFromTasks_SecretMapKeyInsideSlice covers the same shape
// reached through a slice, which a map-only guard would miss.
func TestRedactSecretsFromTasks_SecretMapKeyInsideSlice(t *testing.T) {
	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "run", Type: "core.script", Params: map[string]any{
				"env_sets": []any{
					map[string]any{"{{ Secrets['db_password'] }}": "static-value"},
				},
			}},
		},
	}
	tasks := []model.TaskExecutionDetails{
		{ID: "run", Input: map[string]any{
			"env_sets": []any{map[string]any{"Zx9-canary-7Qw": "static-value"}},
		}},
	}

	RedactSecretsFromTasks(tasks, wfDef)

	assert.Equal(t, RedactedValue, tasks[0].Input["env_sets"])
}

// TestRedactSecretsFromTasks_PlainMapKeysStayLeafPrecise guards the fix above
// from over-reaching: ordinary key names must not trigger container blanking.
func TestRedactSecretsFromTasks_PlainMapKeysStayLeafPrecise(t *testing.T) {
	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "run", Type: "core.script", Params: map[string]any{
				"env": map[string]any{
					"DB_PASSWORD": "{{ Secrets['db_password'] }}",
					"LOG_LEVEL":   "debug",
				},
			}},
		},
	}
	tasks := []model.TaskExecutionDetails{
		{ID: "run", Input: map[string]any{
			"env": map[string]any{"DB_PASSWORD": "Zx9-canary-7Qw", "LOG_LEVEL": "debug"},
		}},
	}

	RedactSecretsFromTasks(tasks, wfDef)

	env, ok := tasks[0].Input["env"].(map[string]any)
	require.True(t, ok, "an ordinary key map must keep its shape")
	assert.Equal(t, RedactedValue, env["DB_PASSWORD"])
	assert.Equal(t, "debug", env["LOG_LEVEL"])
}
