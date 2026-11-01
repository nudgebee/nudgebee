package relay

import (
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"nudgebee/services/config"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func setRelayMaxResponseBytes(t *testing.T, limit int64) {
	t.Helper()
	previous := config.Config.RelayMaxResponseBytes
	config.Config.RelayMaxResponseBytes = limit
	t.Cleanup(func() { config.Config.RelayMaxResponseBytes = previous })
}

func TestReadRelayBody(t *testing.T) {
	for _, tc := range []struct {
		name     string
		limit    int64
		size     int
		tooLarge bool
	}{
		{name: "under the limit", limit: 16, size: 15},
		{name: "exactly the limit", limit: 16, size: 16},
		{name: "one byte over", limit: 16, size: 17, tooLarge: true},
		{name: "cap disabled", limit: 0, size: 4096},
		{name: "limit at the int64 maximum", limit: math.MaxInt64, size: 4096},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setRelayMaxResponseBytes(t, tc.limit)
			data, err := readRelayBody(strings.NewReader(strings.Repeat("x", tc.size)))
			if tc.tooLarge {
				if !errors.Is(err, ErrResponseTooLarge) {
					t.Fatalf("err = %v, want ErrResponseTooLarge", err)
				}
				if data != nil {
					t.Errorf("got %d bytes back with the error, want none", len(data))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(data) != tc.size {
				t.Errorf("read %d bytes, want %d", len(data), tc.size)
			}
		})
	}
}

// TestExecuteRejectsOversizedResponse checks that a response over the cap fails with
// ErrResponseTooLarge after one attempt — asking the agent again would only fetch the
// same oversized answer. The under-the-cap case proves the same relay is otherwise read.
func TestExecuteRejectsOversizedResponse(t *testing.T) {
	var attempts atomic.Int32
	var payload atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload.Load().(string)))
	}))
	defer server.Close()

	endpoint := config.Config.RelayServerEndpoint
	config.Config.RelayServerEndpoint = server.URL
	const accountID = "response-cap-account"
	agentStatusCache.Lock()
	agentStatusCache.entries[accountID] = agentStatusCacheEntry{connected: true, expiresAt: time.Now().Add(time.Minute)}
	agentStatusCache.Unlock()
	t.Cleanup(func() {
		config.Config.RelayServerEndpoint = endpoint
		agentStatusCache.Lock()
		delete(agentStatusCache.entries, accountID)
		agentStatusCache.Unlock()
	})
	setRelayMaxResponseBytes(t, 1024)
	request := RelayExecuteRequest{Body: ActionExecuteBody{AccountID: accountID, ActionName: "prometheus_queries_enricher"}}

	payload.Store(`{"data":{"series":"` + strings.Repeat("x", 512) + `"}}`)
	response, err := Execute(request)
	if err != nil {
		t.Fatalf("response under the cap: unexpected error: %v", err)
	}
	if _, ok := response["data"]; !ok {
		t.Fatalf("response under the cap: data missing from %v", response)
	}

	attempts.Store(0)
	payload.Store(`{"data":{"series":"` + strings.Repeat("x", 2048) + `"}}`)
	_, err = Execute(request)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("response over the cap: err = %v, want ErrResponseTooLarge", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("response over the cap: relay got %d attempts, want 1", got)
	}
}
