package scripting

import (
	"fmt"
	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CapturingLogger implements go.temporal.io/sdk/log.Logger
type CapturingLogger struct {
	Messages []string
}

func (l *CapturingLogger) Debug(msg string, keyvals ...interface{}) {
	l.Messages = append(l.Messages, fmt.Sprintf("DEBUG: %s %v", msg, keyvals))
}

func (l *CapturingLogger) Info(msg string, keyvals ...interface{}) {
	l.Messages = append(l.Messages, fmt.Sprintf("INFO: %s %v", msg, keyvals))
}

func (l *CapturingLogger) Warn(msg string, keyvals ...interface{}) {
	l.Messages = append(l.Messages, fmt.Sprintf("WARN: %s %v", msg, keyvals))
}

func (l *CapturingLogger) Error(msg string, keyvals ...interface{}) {
	l.Messages = append(l.Messages, fmt.Sprintf("ERROR: %s %v", msg, keyvals))
}

func TestRunScriptTask_Security_LogRedaction(t *testing.T) {
	// Force local execution
	oldMode := config.Config.TaskScriptExecutionModel
	config.Config.TaskScriptExecutionModel = "local"
	defer func() { config.Config.TaskScriptExecutionModel = oldMode }()

	logger := &CapturingLogger{Messages: []string{}}
	task := &RunScriptTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), logger)

	secretInScript := "SUPER_SECRET_PASSWORD"
	params := map[string]any{
		"script": fmt.Sprintf("echo '%s'", secretInScript),
		"env": map[string]string{
			"VAR": "VALUE",
		},
	}

	_, err := task.Execute(taskCtx, params)
	require.NoError(t, err)

	// Check logs
	foundSecret := false
	for _, msg := range logger.Messages {
		if strings.Contains(msg, secretInScript) {
			foundSecret = true
			break
		}
	}

	assert.False(t, foundSecret, "Secret should not be present in logs")
}
