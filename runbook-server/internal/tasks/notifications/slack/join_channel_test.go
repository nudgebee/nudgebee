package slack

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSlackJoinChannelTask_Execute(t *testing.T) {
	// Skip test if environment variables are not set
	if os.Getenv("TEST_TENANT_ID") == "" || os.Getenv("TEST_NOTIFICATION_SLACK_CHANNEL_ID") == "" {
		t.Skip("Skipping integration test due to missing environment variables")
	}

	task := &SlackJoinChannelTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

	testCases := []struct {
		name          string
		params        map[string]any
		expectErr     bool
		expectedError string
	}{
		{
			name: "Join Channel Success",
			params: map[string]any{
				"channel_id": os.Getenv("TEST_NOTIFICATION_SLACK_CHANNEL_ID"),
				"text":       "Joining from Runbook Test",
			},
			expectErr: false,
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
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				// Basic check that result is a map (response from API)
				_, ok := result.(map[string]any)
				assert.True(t, ok, "Expected map[string]any response")
			}
		})
	}
}
