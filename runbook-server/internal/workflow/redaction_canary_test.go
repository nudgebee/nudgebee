package workflow

import (
	"encoding/json"
	"strings"
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonapi "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	historyapi "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/converter"
)

// secretCanary is a distinctive value that must never appear in a param
// surface. Anything matching it in a response body is a leak.
const secretCanary = "Zx9-canary-7Qw"

// canaryDefinition covers every param shape a secret can hide in: a direct
// scalar, a value nested under a map, a value nested inside a slice of maps,
// a bare list element, and a bare element one slice deeper.
func canaryDefinition() model.WorkflowDefinition {
	return model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "notify", Type: "http.request", Params: map[string]any{
				"url":        "https://hooks.example.com/abc",
				"auth_token": "{{ Secrets['db_password'] }}",
				"headers": map[string]any{
					"Authorization": "Bearer {{ Secrets['db_password'] }}",
					"Accept":        "application/json",
				},
				"retries": []any{
					map[string]any{"attempt": 1, "fallback_key": "{{ Secrets['db_password'] }}"},
					map[string]any{"attempt": 2, "note": "no secret here"},
				},
				"args": []any{"--token", "{{ Secrets['db_password'] }}"},
				"arg_groups": []any{
					[]any{"--verbose"},
					[]any{"--token", "{{ Secrets['db_password'] }}"},
				},
				"env": map[string]any{"{{ Secrets['db_password'] }}": "static-value"},
			}},
		},
	}
}

// resolveParams runs the definition params through the real templating engine
// with a real Secrets map, so the "resolved" input under test is what the
// executor actually produces rather than something hand-written.
func resolveParams(t *testing.T, params map[string]any) map[string]any {
	t.Helper()
	tplCtx := NewTemplateContext(nil, nil)
	require.NotNil(t, tplCtx)
	tplCtx.Secrets = map[string]any{"db_password": secretCanary}

	resolved, err := ProcessValue(params, tplCtx)
	require.NoError(t, err)
	resolvedMap, ok := resolved.(map[string]any)
	require.True(t, ok)
	return resolvedMap
}

// findCanary walks a task tree and reports every param surface still holding
// the canary, as "<taskID>.<field>".
func findCanary(tasks []model.TaskExecutionDetails, out *[]string) {
	for _, task := range tasks {
		for field, surface := range map[string]map[string]any{
			"input":           task.Input,
			"rendered_params": task.RenderedParams,
		} {
			if surface == nil {
				continue
			}
			encoded, err := json.Marshal(surface)
			if err != nil {
				continue
			}
			if strings.Contains(string(encoded), secretCanary) {
				*out = append(*out, task.ID+"."+field)
			}
		}
		findCanary(task.Children, out)
	}
}

// TestTemplatingActuallyResolvesTheCanary pins the premise of the canary test:
// if the templating engine stopped substituting Secrets, every leak assertion
// below would pass vacuously.
func TestTemplatingActuallyResolvesTheCanary(t *testing.T) {
	resolved := resolveParams(t, canaryDefinition().Tasks[0].Params)

	encoded, err := json.Marshal(resolved)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), secretCanary,
		"templating must resolve {{ Secrets.db_password }} for this suite to mean anything")
}

// TestProcessWorkflowHistoryLeaksNoResolvedSecretIntoParams is the automated
// form of the manual check for this bug: run a task whose params resolve a
// secret, read the execution back the way the API does, and grep every param
// surface for the canary. Zero hits is the pass condition.
//
// Scope note: task Output is deliberately not covered here. A task that echoes
// a secret into its own output is a separate exposure that needs value-based
// scrubbing, which is not part of this change.
func TestProcessWorkflowHistoryLeaksNoResolvedSecretIntoParams(t *testing.T) {
	dc := converter.GetDefaultDataConverter()
	mockTemporalClient := new(MockTemporalClient)
	service := &Service{temporalClient: mockTemporalClient, dataConverter: dc}
	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})

	def := canaryDefinition()
	resolved := resolveParams(t, def.Tasks[0].Params)
	resolved["__tenant_id"] = "test-tenant"

	events := []*historyapi.HistoryEvent{
		{
			EventId:   5,
			EventType: enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
			Attributes: &historyapi.HistoryEvent_ActivityTaskScheduledEventAttributes{
				ActivityTaskScheduledEventAttributes: &historyapi.ActivityTaskScheduledEventAttributes{
					ActivityId:   "notify",
					ActivityType: &commonapi.ActivityType{Name: "http.request"},
					Input:        payloadOf(t, dc, resolved),
				},
			},
		},
		{
			EventId:   6,
			EventType: enums.EVENT_TYPE_ACTIVITY_TASK_COMPLETED,
			Attributes: &historyapi.HistoryEvent_ActivityTaskCompletedEventAttributes{
				ActivityTaskCompletedEventAttributes: &historyapi.ActivityTaskCompletedEventAttributes{
					ScheduledEventId: 5,
					Result:           payloadOf(t, dc, map[string]any{"status": 200}),
				},
			},
		},
	}

	details, err := service.processWorkflowHistory(sc, "test-account", newHistoryIterator(events), def)
	require.NoError(t, err)

	var leaks []string
	findCanary(details.Tasks, &leaks)
	assert.Empty(t, leaks, "resolved secret reached these param surfaces: %v", leaks)

	// Redaction must not have emptied the panel: non-secret siblings survive.
	notify, ok := tasksByID(details.Tasks)["notify"]
	require.True(t, ok)
	assert.Equal(t, "https://hooks.example.com/abc", notify.RenderedParams["url"])
	headers, ok := notify.RenderedParams["headers"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "application/json", headers["Accept"])
}

// TestCanaryFindsALeakWhenRedactionIsAbsent proves findCanary can fail. Without
// it, a walker bug would make the leak test pass for the wrong reason.
func TestCanaryFindsALeakWhenRedactionIsAbsent(t *testing.T) {
	resolved := resolveParams(t, canaryDefinition().Tasks[0].Params)
	unredacted := []model.TaskExecutionDetails{
		{ID: "notify", Input: resolved, RenderedParams: resolved},
	}

	var leaks []string
	findCanary(unredacted, &leaks)

	assert.ElementsMatch(t, []string{"notify.input", "notify.rendered_params"}, leaks)
}

// TestRedactSecretsFromTasksClearsEveryCanarySurface drives the redactor
// directly over all three nesting shapes, including a child task.
func TestRedactSecretsFromTasksClearsEveryCanarySurface(t *testing.T) {
	def := canaryDefinition()
	resolved := resolveParams(t, def.Tasks[0].Params)

	tasks := []model.TaskExecutionDetails{
		{
			ID:             "notify",
			Input:          resolved,
			RenderedParams: resolved,
			Children: []model.TaskExecutionDetails{
				{ID: "notify", Input: resolved, RenderedParams: resolved},
			},
		},
	}

	RedactSecretsFromTasks(tasks, def)

	var leaks []string
	findCanary(tasks, &leaks)
	assert.Empty(t, leaks, "resolved secret reached these param surfaces: %v", leaks)

	// The scalar, the map-nested value and the slice-nested value each blanked.
	assert.Equal(t, RedactedValue, tasks[0].RenderedParams["auth_token"])
	headers, ok := tasks[0].RenderedParams["headers"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, RedactedValue, headers["Authorization"])
	retries, ok := tasks[0].RenderedParams["retries"].([]any)
	require.True(t, ok, "a slice of maps keeps its shape — its keys are nameable")
	firstRetry, ok := retries[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, RedactedValue, firstRetry["fallback_key"])
	assert.EqualValues(t, 1, firstRetry["attempt"])

	// A bare list element, and a map key rendered from a secret, have no key of
	// their own to redact by — the whole containing value blanks.
	assert.Equal(t, RedactedValue, tasks[0].RenderedParams["args"])
	assert.Equal(t, RedactedValue, tasks[0].RenderedParams["arg_groups"])
	assert.Equal(t, RedactedValue, tasks[0].RenderedParams["env"])
}
