package network

import (
	"nudgebee/runbook/internal/tasks/testutils"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNtpTask_Execute(t *testing.T) {
	task := &NtpTask{}
	logger := &TestLogger{}
	taskCtx := testutils.NewTestTaskContext("tenant", "account", "user", logger)

	t.Run("NTP Check (pool.ntp.org)", func(t *testing.T) {
		params := map[string]any{
			"host": "pool.ntp.org",
		}

		res, err := task.Execute(taskCtx, params)
		if err != nil {
			// UDP port 123 might be blocked in some envs
			t.Logf("NTP check failed (network?): %v", err)
			return
		}

		resultMap, ok := res.(map[string]any)
		assert.True(t, ok)
		assert.NotEmpty(t, resultMap["ntp_time"])

		drift, ok := resultMap["drift_seconds"].(float64)
		assert.True(t, ok)
		// Drift should be reasonably small (e.g. < 60s) unless local clock is way off
		assert.True(t, drift > -60 && drift < 60, "Drift %f is unusually high", drift)
	})
}
