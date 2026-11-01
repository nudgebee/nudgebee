package tools

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestMetricFilterUsageIDs(t *testing.T) {
	conv, msg := uuid.NewString(), uuid.NewString()
	original := conversationMessageExists
	t.Cleanup(func() { conversationMessageExists = original })

	calls := 0
	conversationMessageExists = func(c, m string) (bool, error) {
		calls++
		if c == conv && m == msg {
			return true, nil
		}
		return false, nil
	}

	gotConv, gotMsg := metricFilterUsageIDs(conv, msg)
	assert.Equal(t, conv, gotConv)
	assert.Equal(t, msg, gotMsg)

	// A lookup failure falls back to recording the call unattributed.
	conversationMessageExists = func(string, string) (bool, error) { return false, errors.New("db down") }
	gotConv, gotMsg = metricFilterUsageIDs(conv, msg)
	assert.Empty(t, gotConv)
	assert.Empty(t, gotMsg)
	conversationMessageExists = func(c, m string) (bool, error) {
		calls++
		return c == conv && m == msg, nil
	}

	// A watch source's synthetic ids are valid UUIDs with no rows behind them;
	// billing to them would fail the foreign key and lose the usage row.
	synthetic := uuid.NewString()
	gotConv, gotMsg = metricFilterUsageIDs(synthetic, synthetic)
	assert.Empty(t, gotConv)
	assert.Empty(t, gotMsg)

	before := calls
	for _, ids := range [][2]string{{"", ""}, {conv, ""}, {"not-a-uuid", msg}} {
		gotConv, gotMsg = metricFilterUsageIDs(ids[0], ids[1])
		assert.Empty(t, gotConv, ids)
		assert.Empty(t, gotMsg, ids)
	}
	assert.Equal(t, before, calls, "non-UUID ids must not hit the database")
}
