package notifications

import (
	"log/slog"
	// "nudgebee/runbook/internal/tasks/types" // Removed unused import
	"nudgebee/runbook/internal/tasks/testutils" // Updated import path
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNotificationTask_Execute(t *testing.T) {
	task := &ImSendTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

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
				"message":  "Hello",
				"channel":  os.Getenv("TEST_NOTIFICATION_SLACK_CHANNEL_ID"),
				"provider": "slack",
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
				assert.NotNil(t, result)
				slackResponse := result.(map[string]any)
				assert.NotNil(t, slackResponse["message_id"])

				tc.params["message_thread_id"] = slackResponse["message_id"]
				tc.params["team_id"] = slackResponse["team"]
				result, err := task.Execute(taskCtx, tc.params)
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}
