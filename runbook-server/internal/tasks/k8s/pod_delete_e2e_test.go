//go:build e2e

package k8s

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPodDeleteTask_Execute_Cluster covers the rows that reach a live
// relay/cluster (kubectl). It requires the cluster env vars to be set.
func TestPodDeleteTask_Execute_Cluster(t *testing.T) {
	testutils.RequireEnv(t, "TEST_TENANT_ID", "TEST_K8S_ACCOUNT_ID", "TEST_USER_ID")

	task := &PodDeleteTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_K8S_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

	testCases := []struct {
		name          string
		params        map[string]any
		expectErr     bool
		expectedError string
	}{
		{
			name:          "Kind defaults to Pod and pod not found",
			params:        map[string]any{"namespace": "default", "name": "nonexistent-pod"},
			expectErr:     true,
			expectedError: "pods \"nonexistent-pod\" not found", // Expect error from kubectl
		},
		{
			name:          "Valid Pod Deletion with Force (expect kubectl error for non-existent pod)",
			params:        map[string]any{"namespace": "default", "name": "nonexistent-pod-force", "force": true},
			expectErr:     true,
			expectedError: "pods \"nonexistent-pod-force\" not found", // Expect error from kubectl as pod won't exist
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := task.Execute(taskCtx, tc.params)
			if tc.expectErr {
				assert.Error(t, err)
				if tc.expectedError != "" {
					assert.Contains(t, err.Error(), tc.expectedError)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
