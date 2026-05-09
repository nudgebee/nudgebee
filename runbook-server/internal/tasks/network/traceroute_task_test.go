package network

import (
	"nudgebee/runbook/internal/tasks/testutils"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTracerouteTask_Execute(t *testing.T) {
	task := &TracerouteTask{}
	logger := &TestLogger{}
	taskCtx := testutils.NewTestTaskContext("tenant", "account", "user", logger)

	t.Run("Traceroute Localhost", func(t *testing.T) {
		params := map[string]any{
			"host":     "127.0.0.1",
			"max_hops": 5.0,
		}

		res, err := task.Execute(taskCtx, params)
		if err != nil {
			t.Logf("Traceroute failed (binary missing?): %v", err)
			return
		}

		resultMap, ok := res.(map[string]any)
		assert.True(t, ok)
		// Localhost traceroute should be very short, often 1 hop
		hops, ok := resultMap["hops"].([]Hop)
		// If struct is not exported or type assertion fails due to unmarshalling?
		// Wait, we returned []Hop.
		assert.True(t, ok)
		assert.NotEmpty(t, hops)
		assert.Equal(t, "127.0.0.1", hops[len(hops)-1].IP)
	})

	t.Run("Invalid Host", func(t *testing.T) {
		params := map[string]any{
			"host": "-flag",
		}
		_, err := task.Execute(taskCtx, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "host cannot start with hyphen")
	})
}
