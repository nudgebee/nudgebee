//go:build e2e

package k8s

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPVRightsizeTask_Execute_Cluster covers the rows that reach a live
// relay/cluster (PVC fetch). It requires the cluster env vars to be set.
func TestPVRightsizeTask_Execute_Cluster(t *testing.T) {
	testutils.RequireEnv(t, "TEST_TENANT_ID", "TEST_K8S_ACCOUNT_ID", "TEST_USER_ID")

	task := &PVRightsizeTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_K8S_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

	testCases := []struct {
		name          string
		params        map[string]any
		expectErr     bool
		expectedError string
	}{
		{
			// kind is optional and defaults to PersistentVolumeClaim, so an
			// omitted kind is valid and the task proceeds to fetch the PVC,
			// which requires a live relay/cluster.
			name:          "Missing Kind defaults to PVC and fetches it",
			params:        map[string]any{"namespace": "default", "name": "test-pvc", "change_to": "1Gi"},
			expectErr:     true,
			expectedError: "failed to fetch PVC",
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
