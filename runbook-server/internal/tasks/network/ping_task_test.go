package network

import (
	"nudgebee/runbook/internal/tasks/testutils"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPingTask_Execute(t *testing.T) {
	task := &PingTask{}
	logger := &TestLogger{}
	taskCtx := testutils.NewTestTaskContext("tenant", "account", "user", logger)

	t.Run("Ping Localhost", func(t *testing.T) {
		params := map[string]any{
			"host":  "127.0.0.1",
			"count": 1.0,
		}

		res, err := task.Execute(taskCtx, params)
		if err != nil {
			// Ping might fail in some CI environments or containers without 'ping' binary
			t.Logf("Ping failed (expected if binary missing): %v", err)
			return
		}

		resultMap, ok := res.(map[string]any)
		assert.True(t, ok)
		assert.True(t, resultMap["reachable"].(bool))
		// Packet loss should be 0
		assert.Equal(t, 0.0, resultMap["packet_loss"])
	})
}
