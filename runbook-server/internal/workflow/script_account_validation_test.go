package workflow

import (
	"errors"
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
)

// Saving refuses a script step that could never run on the executor it names,
// and leaves alone anything that only resolves at run time.
func TestValidateScriptAccount(t *testing.T) {
	providers := map[string]string{"cluster": "K8s", "aws-acct": "AWS"}
	orig := accountProvider
	accountProvider = func(_ *security.RequestContext, id string) (string, error) {
		if p, ok := providers[id]; ok {
			return p, nil
		}
		return "", errors.New("account not found")
	}
	t.Cleanup(func() { accountProvider = orig })

	step := func(params map[string]any) model.Task {
		return model.Task{ID: "diag", Type: "scripting.run_script", Params: params}
	}
	for _, tc := range []struct {
		name       string
		workflowAc string
		params     map[string]any
		want       string // substring of the error; "" means valid
	}{
		{"cluster account on Nudgebee's runner", "", map[string]any{"account_id": "cluster", "executor_type": "kubernetes"}, "task 'diag': this step's account is a Kubernetes cluster"},
		{"falls back to the workflow's account", "cluster", map[string]any{"executor_type": "kubernetes"}, "Kubernetes cluster"},
		{"cluster account on the agent", "", map[string]any{"account_id": "cluster", "executor_type": "agent"}, ""},
		{"cloud account on Nudgebee's runner", "", map[string]any{"account_id": "aws-acct", "executor_type": "kubernetes"}, ""},
		{"no executor named", "", map[string]any{"account_id": "cluster"}, ""},
		{"templated executor", "", map[string]any{"account_id": "cluster", "executor_type": "{{ Inputs.exec }}"}, ""},
		{"templated account", "", map[string]any{"account_id": "{{ Inputs.acct }}", "executor_type": "kubernetes"}, ""},
		{"unknown account is left to the account check", "", map[string]any{"account_id": "gone", "executor_type": "kubernetes"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateScriptAccount(nil, tc.workflowAc, step(tc.params))
			if tc.want == "" {
				assert.NoError(t, err)
				return
			}
			if assert.Error(t, err) {
				assert.Contains(t, err.Error(), tc.want)
			}
		})
	}
}
