package tickets

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTicketsAddCommentTask_Execute(t *testing.T) {
	task := &TicketsAddCommentTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

	var createdTicketID string
	configID := os.Getenv("TEST_TICKET_INTEGRATION_ID")

	// Try to create a ticket if we have configuration
	if configID != "" {
		createTask := &TicketsCreateTask{}
		createParams := map[string]any{
			"configuration_id": configID,
			"title":            "Sample Ticket for Comment Test",
			"description":      "Ticket created to test comment addition",
			"project_key":      "nudgebee/nudgebee",
		}
		// We ignore error here, if it fails, we just skip the success test case
		res, err := createTask.Execute(taskCtx, createParams)
		if err == nil {
			if resMap, ok := res.(map[string]any); ok {
				if val, ok := resMap["ticket_id"].(string); ok {
					createdTicketID = val
				}
			}
		}
	}

	testCases := []struct {
		name          string
		params        map[string]any
		expectErr     bool
		expectedError string
		skip          bool
	}{
		{
			name: "Missing Ticket ID",
			params: map[string]any{
				"comment": "Test Comment",
			},
			expectErr:     true,
			expectedError: "ticket_id is required",
		},
		{
			name: "Missing Comment",
			params: map[string]any{
				"ticket_id": "123",
			},
			expectErr:     true,
			expectedError: "comment is required",
		},
		{
			name: "Invalid integration_id type",
			params: map[string]any{
				"ticket_id":      "123",
				"comment":        "test",
				"integration_id": 123, // should be string
			},
			expectErr: true,
			// integration_id is read via extractRequiredString, which treats a
			// non-string (failed type assertion) the same as a missing value.
			expectedError: "integration_id is required",
		},
		{
			name: "Add Comment Success",
			params: map[string]any{
				"integration_id": configID,
				"ticket_id":      createdTicketID,
				"comment":        "Adding a test comment",
			},
			skip: createdTicketID == "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skip {
				t.Skip("Skipping test case due to missing prerequisites")
			}
			result, err := task.Execute(taskCtx, tc.params)

			if tc.expectErr {
				assert.Error(t, err)
				if tc.expectedError != "" {
					assert.Contains(t, err.Error(), tc.expectedError)
				}
			} else {
				if !assert.NoError(t, err) {
					return
				}
				resMap, ok := result.(map[string]any)
				if assert.True(t, ok, "Result should be a map") {
					assert.NotNil(t, resMap["ticket_id"])
					assert.NotNil(t, resMap["comments"])
				}
			}
		})
	}
}
