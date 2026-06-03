package notifications

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReadThreadTask_GetName(t *testing.T) {
	task := &ReadThreadTask{}
	assert.Equal(t, "notifications.read_thread", task.GetName())
}

func TestReadThreadTask_GetDescription(t *testing.T) {
	task := &ReadThreadTask{}
	assert.NotEmpty(t, task.GetDescription())
}

func TestReadThreadTask_GetDisplayName(t *testing.T) {
	task := &ReadThreadTask{}
	assert.NotEmpty(t, task.GetDisplayName())
}

func TestReadThreadTask_InputSchema(t *testing.T) {
	task := &ReadThreadTask{}
	schema := task.InputSchema()

	assert.NotNil(t, schema)
	assert.NotNil(t, schema.Properties)

	// Verify required properties exist
	assert.Contains(t, schema.Properties, "provider")
	assert.Contains(t, schema.Properties, "channel_id")
	assert.Contains(t, schema.Properties, "thread_ts")
	assert.Contains(t, schema.Properties, "team_id")

	// Verify required flags
	assert.True(t, schema.Properties["provider"].Required)
	assert.True(t, schema.Properties["channel_id"].Required)
	assert.True(t, schema.Properties["thread_ts"].Required)
	assert.False(t, schema.Properties["team_id"].Required)
}

func TestReadThreadTask_OutputSchema(t *testing.T) {
	task := &ReadThreadTask{}
	schema := task.OutputSchema()

	assert.NotNil(t, schema)
	assert.NotNil(t, schema.Properties)

	// Verify output properties exist
	assert.Contains(t, schema.Properties, "success")
	assert.Contains(t, schema.Properties, "error")
	assert.Contains(t, schema.Properties, "messages")
	assert.Contains(t, schema.Properties, "has_responses")
	assert.Contains(t, schema.Properties, "has_reactions")
	assert.Contains(t, schema.Properties, "reply_count")
}

func TestReadThreadTask_Execute_MissingProvider(t *testing.T) {
	task := &ReadThreadTask{}
	taskCtx := testutils.NewTestTaskContext("test-tenant", "test-account", "test-user", slog.Default())

	params := map[string]any{
		"channel_id": "C123456",
		"thread_ts":  "1234567890.123456",
	}

	_, err := task.Execute(taskCtx, params)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "provider is required")
}

func TestReadThreadTask_Execute_UnsupportedProvider(t *testing.T) {
	task := &ReadThreadTask{}
	taskCtx := testutils.NewTestTaskContext("test-tenant", "test-account", "test-user", slog.Default())

	params := map[string]any{
		"provider":   "teams",
		"channel_id": "C123456",
		"thread_ts":  "1234567890.123456",
	}

	_, err := task.Execute(taskCtx, params)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "only slack provider is supported")
}

func TestReadThreadTask_Execute_MissingChannelID(t *testing.T) {
	task := &ReadThreadTask{}
	taskCtx := testutils.NewTestTaskContext("test-tenant", "test-account", "test-user", slog.Default())

	params := map[string]any{
		"provider":  "slack",
		"thread_ts": "1234567890.123456",
	}

	_, err := task.Execute(taskCtx, params)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "channel_id is required")
}

func TestReadThreadTask_Execute_MissingThreadTs(t *testing.T) {
	task := &ReadThreadTask{}
	taskCtx := testutils.NewTestTaskContext("test-tenant", "test-account", "test-user", slog.Default())

	params := map[string]any{
		"provider":   "slack",
		"channel_id": "C123456",
	}

	_, err := task.Execute(taskCtx, params)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "thread_ts is required")
}

// Integration test - requires environment variables to be set
func TestReadThreadTask_Execute_Integration(t *testing.T) {
	// Skip test if environment variables are not set
	if os.Getenv("TEST_TENANT_ID") == "" || os.Getenv("TEST_NOTIFICATION_SLACK_CHANNEL_ID") == "" {
		t.Skip("Skipping integration test due to missing environment variables")
	}

	// First, send a message to create a thread
	imTask := &ImSendTask{}
	taskCtx := testutils.NewTestTaskContext(
		os.Getenv("TEST_TENANT_ID"),
		os.Getenv("TEST_ACCOUNT_ID"),
		os.Getenv("TEST_USER_ID"),
		slog.Default(),
	)

	// Send initial message
	sendParams := map[string]any{
		"message":  "Test thread for read_thread integration test",
		"channel":  os.Getenv("TEST_NOTIFICATION_SLACK_CHANNEL_ID"),
		"provider": "slack",
	}

	sendResult, err := imTask.Execute(taskCtx, sendParams)
	if err != nil {
		t.Skipf("Skipping integration test - could not send test message: %v", err)
	}

	sendResponse := sendResult.(map[string]any)
	messageTs := sendResponse["message_id"].(string)
	channelID := sendResponse["channel"].(string)

	// Now read the thread
	readTask := &ReadThreadTask{}
	readParams := map[string]any{
		"provider":   "slack",
		"channel_id": channelID,
		"thread_ts":  messageTs,
	}

	if teamID, ok := sendResponse["team"].(string); ok && teamID != "" {
		readParams["team_id"] = teamID
	}

	result, err := readTask.Execute(taskCtx, readParams)
	assert.NoError(t, err)
	assert.NotNil(t, result)

	response := result.(map[string]any)
	assert.True(t, response["success"].(bool))
	assert.Equal(t, "", response["error"].(string))
	assert.NotNil(t, response["messages"])

	messages := response["messages"].([]map[string]any)
	assert.GreaterOrEqual(t, len(messages), 1, "Should have at least the parent message")

	// Verify parent message is present
	hasParent := false
	for _, msg := range messages {
		if isParent, ok := msg["is_parent"].(bool); ok && isParent {
			hasParent = true
			break
		}
	}
	assert.True(t, hasParent, "Should have a parent message")
}

// Integration test for reading a thread with reactions
func TestReadThreadTask_Execute_WithReactions(t *testing.T) {
	// Skip test if environment variables are not set
	if os.Getenv("TEST_TENANT_ID") == "" || os.Getenv("TEST_NOTIFICATION_SLACK_CHANNEL_ID") == "" {
		t.Skip("Skipping integration test due to missing environment variables")
	}

	// This test requires a pre-existing thread with reactions
	// Set TEST_THREAD_TS and TEST_THREAD_CHANNEL_ID to test with reactions
	threadTs := os.Getenv("TEST_THREAD_TS")
	channelID := os.Getenv("TEST_THREAD_CHANNEL_ID")
	if threadTs == "" || channelID == "" {
		t.Skip("Skipping reactions test - TEST_THREAD_TS and TEST_THREAD_CHANNEL_ID not set")
	}

	task := &ReadThreadTask{}
	taskCtx := testutils.NewTestTaskContext(
		os.Getenv("TEST_TENANT_ID"),
		os.Getenv("TEST_ACCOUNT_ID"),
		os.Getenv("TEST_USER_ID"),
		slog.Default(),
	)

	params := map[string]any{
		"provider":   "slack",
		"channel_id": channelID,
		"thread_ts":  threadTs,
	}

	result, err := task.Execute(taskCtx, params)
	assert.NoError(t, err)
	assert.NotNil(t, result)

	response := result.(map[string]any)
	assert.True(t, response["success"].(bool))

	// Log the response for manual verification
	t.Logf("Thread response: has_responses=%v, has_reactions=%v, reply_count=%v",
		response["has_responses"],
		response["has_reactions"],
		response["reply_count"],
	)
}
