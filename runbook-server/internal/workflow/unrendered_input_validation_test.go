package workflow

import (
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateUnrenderedInputs(t *testing.T) {
	schedule := func(params map[string]any) []model.Trigger {
		return []model.Trigger{{Type: model.WorkflowTriggerManual}, {Type: model.WorkflowTriggerSchedule, Params: params}}
	}
	for _, tc := range []struct {
		name     string
		inputs   []model.Input
		triggers []model.Trigger
		want     string // substring of the error; "" means valid
	}{
		{"plain default", []model.Input{{ID: "slack_channel", Default: "C0123"}}, nil, ""},
		{"no default", []model.Input{{ID: "slack_channel"}}, nil, ""},
		{"non-string defaults", []model.Input{{ID: "n", Default: 25.0}, {ID: "b", Default: true}}, nil, ""},
		{"config", []model.Input{{ID: "slack_channel", Default: "{{ Configs.slack_channel }}"}}, nil, `slack_channel: "{{ Configs.slack_channel }}"`},
		{"no spaces", []model.Input{{ID: "c", Default: "{{Configs.c}}"}}, nil, `c: "{{Configs.c}}"`},
		{"bracket form", []model.Input{{ID: "c", Default: "{{ Configs['c'] }}"}}, nil, "c: "},
		{"secret in text", []model.Input{{ID: "auth", Default: "Bearer {{ Secrets.token }}"}}, nil, "auth: "},
		{"another input", []model.Input{{ID: "a", Default: "x"}, {ID: "b", Default: "{{ Inputs.a }}-suffix"}}, nil, "b: "},
		{"built-in", []model.Input{{ID: "since", Default: "{{ now() | time_add('-1h') }}"}}, nil, "since: "},
		{"statement", []model.Input{{ID: "s", Default: "{% if Vars.x %}a{% endif %}"}}, nil, "s: "},
		{"in a json default", []model.Input{{ID: "route", Type: "json", Default: map[string]any{"to": map[string]any{"channel": "{{ Configs.slack_channel }}"}}}}, nil, `route: "{{ Configs.slack_channel }}"`},
		{"in an array default", []model.Input{{ID: "chans", Type: "array", Default: []any{"C0123", "{{ Configs.slack_channel }}"}}}, nil, "chans: "},
		{"every input named", []model.Input{{ID: "a", Default: "{{ Configs.a }}"}, {ID: "b", Default: "{{ Configs.b }}"}}, nil, `a: "{{ Configs.a }}", b: "{{ Configs.b }}"`},
		{"prometheus template", []model.Input{{ID: "rule", Type: "json", Default: map[string]any{"summary": "{{ $labels.pod }} restarted {{ $value }} times"}}}, nil, ""},
		{"helm template", []model.Input{{ID: "img", Default: "{{ .Values.image.tag }}"}}, nil, ""},
		{"go template", []model.Input{{ID: "fmt", Default: `{{range .items}}{{.metadata.name}}{{"\n"}}{{end}}`}}, nil, ""},
		{"github actions expression", []model.Input{{ID: "env", Default: "${{ secrets.TOKEN }}"}}, nil, ""},
		{"filter named like a built-in", []model.Input{{ID: "f", Default: "{{ x | date_format('2006') }}"}}, nil, ""},
		{"engine name in a quoted string", []model.Input{{ID: "f", Default: `{{ x | default("now") }}`}}, nil, ""},
		{"sprig pipeline", []model.Input{{ID: "y", Default: `{{ now | date "2006" }}`}}, nil, ""},
		{"helm pipeline", []model.Input{{ID: "y", Default: `{{ .Values.t | date "2006-01-02" }}`}}, nil, ""},
		{"go template condition", []model.Input{{ID: "g", Default: `{{ if eq .kind "Self" }}x{{ end }}`}}, nil, ""},
		{"handlebars block", []model.Input{{ID: "h", Default: "{{#each Tasks}}{{name}}{{/each}}"}}, nil, ""},
		{"raw span", []model.Input{{ID: "r", Default: "{% raw %}{{ Configs.x }}{% endraw %}"}}, nil, ""},
		{"bare input name is not caught", []model.Input{{ID: "a", Default: "{{ event.title }}"}}, nil, ""},
		{"schedule settings and plain inputs", nil, schedule(map[string]any{"cron": "0 9 * * *", "overlap_policy": "Skip", "limit": 10, "ns": "default"}), ""},
		{"schedule input", nil, schedule(map[string]any{"cron": "0 9 * * *", "slack_channel": "{{ Configs.slack_channel }}"}), `schedule trigger index 1, slack_channel: "{{ Configs.slack_channel }}"`},
		{"schedule input nested", nil, schedule(map[string]any{"cron": "0 9 * * *", "target": map[string]any{"ns": "{{ Vars.ns }}"}}), "schedule trigger index 1, target: "},
		{"event filter is rendered, not an input", nil, []model.Trigger{{Type: model.WorkflowTriggerEvent, Params: map[string]any{"event_type": "x", "filter": "{{ Inputs.event.priority == 'HIGH' }}"}}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateUnrenderedInputs(model.WorkflowDefinition{Inputs: tc.inputs, Triggers: tc.triggers})
			if tc.want == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Contains(t, err.Error(), "never rendered")
			if tc.triggers == nil {
				// The editor pins an error naming a trigger, or each ';'-separated part, to a node.
				assert.NotContains(t, err.Error(), "trigger")
				assert.NotContains(t, err.Error(), ";")
			}
		})
	}
}

// The editor, the automation builder and every save reach the check through ValidateWorkflow.
func TestValidateWorkflowRefusesATemplatedInputDefault(t *testing.T) {
	service := NewService(nil, new(MockWorkflowStore), nil, tasks.NewInitializedTaskRegistry(), nil, nil)
	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})
	wf := model.Workflow{
		Name: "pvc-cleanup",
		Definition: model.WorkflowDefinition{
			Version:  "v1",
			Inputs:   []model.Input{{ID: "slack_channel", Type: "string", Default: "{{ Configs.slack_channel }}"}},
			Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
			Tasks:    []model.Task{{ID: "say", Type: "core.print", Params: map[string]any{"message": "{{ Inputs.slack_channel }}"}}},
		},
	}

	err := service.ValidateWorkflow(sc, "test-account", wf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `slack_channel: "{{ Configs.slack_channel }}"`)
	assert.Contains(t, err.Error(), "{{ Inputs.slack_channel or Configs.slack_channel }}")

	wf.Definition.Inputs[0].Default = "C0123"
	assert.NoError(t, service.ValidateWorkflow(sc, "test-account", wf))
}

// The fix keeps the input as an override: no default, and the step falls back to the template.
func TestValidateUnrenderedInputs_NamesTheFallback(t *testing.T) {
	inputs := func(id, def string) model.WorkflowDefinition {
		return model.WorkflowDefinition{Inputs: []model.Input{{ID: id, Default: def}}}
	}
	for _, tc := range []struct {
		name string
		def  model.WorkflowDefinition
		want string
	}{
		{"one block", inputs("slack_channel", "{{ Configs.slack_channel }}"),
			"drop the default, leave the input not required, and have the step that reads the input fall back to the template instead, as in {{ Inputs.slack_channel or Configs.slack_channel }}"},
		{"whitespace control", inputs("c", "{{- Configs.c -}}"), "as in {{ Inputs.c or Configs.c }}"},
		{"id that is not a name", inputs("slack-channel", "{{ Configs.slack_channel }}"), `as in {{ Inputs["slack-channel"] or Configs.slack_channel }}`},
		{"template inside text", inputs("c", "#{{ Configs.c }}"), "and put the template in the params of the step that reads the input instead"},
		{"two blocks", inputs("c", "{{ Configs.a }}-{{ Configs.b }}"), "and put the template in the params of the step that reads the input instead"},
		{"inside a json default", model.WorkflowDefinition{Inputs: []model.Input{{ID: "route", Type: "json", Default: map[string]any{"channel": "{{ Configs.c }}"}}}},
			"and put the template in the params of the step that reads the input instead"},
		{"schedule input", model.WorkflowDefinition{Triggers: []model.Trigger{{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"cron": "0 9 * * *", "slack_channel": "{{ Configs.slack_channel }}"}}}},
			"stop passing it from the schedule, and have the step that reads the input fall back to the template instead, as in {{ Inputs.slack_channel or Configs.slack_channel }}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateUnrenderedInputs(tc.def)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}
