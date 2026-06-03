package cicd

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestArgoCliTask_Execute(t *testing.T) {
	task := &ArgoCDCliTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_K8S_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

	testCases := []struct {
		name          string
		params        map[string]any
		expected      any
		expectErr     bool
		expectedError string
	}{
		{
			name: "Simple Command Execution",
			params: map[string]any{
				"command":        "argocd version --client",
				"integration_id": os.Getenv("TEST_ARGOCD_INTEGRATION_ID"),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Clean up any potential temporary files from previous runs
			result, err := task.Execute(taskCtx, tc.params)

			if tc.expectErr {
				assert.Error(t, err)
				if tc.expectedError != "" {
					assert.Contains(t, err.Error(), tc.expectedError)
				}
			} else {
				assert.NoError(t, err)
				assert.True(t, strings.HasPrefix(result.(map[string]any)["data"].(string), "argocd"))
			}
		})
	}
}
