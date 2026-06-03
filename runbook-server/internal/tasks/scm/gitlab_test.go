package scm

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGitlabTask_Execute(t *testing.T) {
	task := &GitlabCliTask{}
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
				"command":        "glab --version",
				"integration_id": os.Getenv("TEST_GITLAB_INTEGRATION_ID"),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := task.Execute(taskCtx, tc.params)

			if tc.expectErr {
				assert.Error(t, err)
				if tc.expectedError != "" {
					assert.Contains(t, err.Error(), tc.expectedError)
				}
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.True(t, strings.Contains(result.(map[string]any)["data"].(string), "glab"))
			}
		})
	}
}
