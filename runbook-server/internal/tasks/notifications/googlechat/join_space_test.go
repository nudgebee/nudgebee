package googlechat

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGoogleChatJoinSpaceTask_Metadata(t *testing.T) {
	task := &GoogleChatJoinSpaceTask{}
	assert.Equal(t, "google_chat.join_space", task.GetName())
	assert.Equal(t, "Google Chat Join Space", task.GetDisplayName())

	prop, ok := task.InputSchema().Properties["channel_id"]
	assert.True(t, ok, "channel_id input must be defined")
	assert.True(t, prop.Required, "channel_id must be required")
}

func TestGoogleChatJoinSpaceTask_RequiresChannelID(t *testing.T) {
	task := &GoogleChatJoinSpaceTask{}
	taskCtx := testutils.NewTestTaskContext("t1", "a1", "u1", slog.Default())

	_, err := task.Execute(taskCtx, map[string]any{"text": "hello"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "channel_id is required")
}

func TestGoogleChatJoinSpaceTask_Execute(t *testing.T) {
	// Skip test if environment variables are not set
	if os.Getenv("TEST_TENANT_ID") == "" || os.Getenv("TEST_NOTIFICATION_GOOGLE_CHAT_SPACE_ID") == "" {
		t.Skip("Skipping integration test due to missing environment variables")
	}

	task := &GoogleChatJoinSpaceTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

	result, err := task.Execute(taskCtx, map[string]any{
		"channel_id": os.Getenv("TEST_NOTIFICATION_GOOGLE_CHAT_SPACE_ID"),
		"text":       "Joining from Runbook Test",
	})
	assert.NoError(t, err)
	assert.NotNil(t, result)
	_, ok := result.(map[string]any)
	assert.True(t, ok, "Expected map[string]any response")
}
