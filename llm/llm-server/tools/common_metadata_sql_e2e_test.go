//go:build e2e

package tools

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// TestEventsExecuteLargeLimitStaysBounded: a model-written SELECT * ... LIMIT 100000 on
// TEST_ACCOUNT's real events (read only) must come back cut, say so, and stay bounded (#40194).
func TestEventsExecuteLargeLimitStaysBounded(t *testing.T) {
	account := os.Getenv("TEST_ACCOUNT")
	if account == "" {
		t.Skip("TEST_ACCOUNT not set")
	}
	nbCtx := core.NbToolContext{
		AccountId: account,
		Ctx:       security.NewRequestContext(context.Background(), nil, nil, nil, nil),
	}

	var peak atomic.Uint64
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	base := ms.HeapAlloc
	done := make(chan struct{})
	go func() {
		var s runtime.MemStats
		for {
			select {
			case <-done:
				return
			case <-time.After(5 * time.Millisecond):
				runtime.ReadMemStats(&s)
				if s.HeapAlloc > peak.Load() {
					peak.Store(s.HeapAlloc)
				}
			}
		}
	}()

	resp, err := EventsExecuteTool{}.Call(nbCtx, core.NBToolCallRequest{Command: "SELECT * FROM events ORDER BY starts_at DESC LIMIT 100000"})
	close(done)
	require.NoError(t, err)

	var got struct {
		Rows     []map[string]any `json:"rows"`
		Returned int              `json:"returned"`
		HasMore  bool             `json:"has_more"`
		Note     string           `json:"note"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.Data), &got), "a cut result is an object with rows and a note")
	growth := int64(peak.Load()) - int64(base)
	t.Logf("returned=%d has_more=%v data=%d bytes peak heap growth=%d MB", got.Returned, got.HasMore, len(resp.Data), growth>>20)
	t.Logf("note: %s", got.Note)

	assert.True(t, got.HasMore)
	assert.LessOrEqual(t, got.Returned, sqlToolMaxRows)
	assert.Len(t, got.Rows, got.Returned)
	assert.Contains(t, got.Note, "OFFSET")
	// The rows held are at most sqlToolMaxBytes plus one row; the bound leaves
	// room for the JSON copies events_execute makes of them.
	assert.Less(t, growth, int64(128<<20), "peak heap growth must stay bounded")
}
