package workflow

import (
	"testing"

	"nudgebee/runbook/internal/model"

	"github.com/stretchr/testify/assert"
)

func codesOf(ws []LintWarning) []string {
	var out []string
	for _, w := range ws {
		out = append(out, w.Code+":"+w.TaskID)
	}
	return out
}

func TestLintDefinition(t *testing.T) {
	script := func(id, body string, env map[string]any) model.Task {
		return model.Task{ID: id, Type: "scripting.run_script", Params: map[string]any{"script": body, "env": env}}
	}
	for _, tc := range []struct {
		name  string
		tasks []model.Task
		want  []string
	}{
		{"clean definition", []model.Task{
			script("pods", "kubectl get pods", nil),
			{ID: "cli", Type: "aws.cli", Params: map[string]any{"command": "aws sts assume-role --role-arn x"}},
		}, nil},
		{"script assumes a role", []model.Task{
			script("diag", "creds=$(aws sts assume-role --role-arn \"$R\" --role-session-name d)", nil),
		}, []string{"script_login_code:diag"}},
		{"keys passed through env", []model.Task{
			script("tf", "terraform apply", map[string]any{"AWS_ACCESS_KEY_ID": "{{ Secrets.k }}"}),
		}, []string{"script_login_code:tf"}},
		{"nested in a group", []model.Task{
			{ID: "grp", Type: "core.group", Tasks: []model.Task{script("inner", "aws eks update-kubeconfig --name c", nil)}},
		}, []string{"script_login_code:inner"}},
		{"branch depends on its switch", []model.Task{
			{ID: "start", Type: "core.print"},
			{ID: "route", Type: "core.switch", DependsOn: []string{"start"}, Params: map[string]any{
				"cases": []any{map[string]any{"value": "a", "next": "branch-a"}}, "default_next": "branch-b"}},
			{ID: "branch-a", Type: "core.print", DependsOn: []string{"route"}},
			{ID: "branch-b", Type: "core.print"},
		}, []string{"switch_target_depends_on:branch-a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := LintDefinition(tc.tasks)
			assert.NotNil(t, got, "never nil, so the API serialises []")
			assert.Equal(t, tc.want, codesOf(got))
		})
	}
}

func TestLintDefinition_MessageNamesTheFix(t *testing.T) {
	ws := LintDefinition([]model.Task{{ID: "diag", Type: "scripting.run_script",
		Params: map[string]any{"script": "aws sts assume-role --role-arn x --role-session-name y"}}})
	if assert.Len(t, ws, 1) {
		assert.Contains(t, ws[0].Message, "sts assume-role")
		assert.Contains(t, ws[0].Message, "remove the login")
	}
}
