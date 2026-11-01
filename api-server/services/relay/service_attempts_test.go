package relay

import (
	"net/http"
	"net/http/httptest"
	"nudgebee/services/config"
	"sync/atomic"
	"testing"
	"time"
)

// TestExecuteMaxAttempts checks that a caller working to a deadline can turn off the
// retries. The relay here drops every connection mid-request — a transient error Execute
// retries — and the default case proves the harness does trigger them.
func TestExecuteMaxAttempts(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
			_ = conn.Close()
		}
	}))
	defer server.Close()

	endpoint := config.Config.RelayServerEndpoint
	config.Config.RelayServerEndpoint = server.URL
	const accountID = "max-attempts-account"
	agentStatusCache.Lock()
	agentStatusCache.entries[accountID] = agentStatusCacheEntry{connected: true, expiresAt: time.Now().Add(time.Minute)}
	agentStatusCache.Unlock()
	t.Cleanup(func() {
		config.Config.RelayServerEndpoint = endpoint
		agentStatusCache.Lock()
		delete(agentStatusCache.entries, accountID)
		agentStatusCache.Unlock()
	})

	for _, tc := range []struct {
		maxAttempts int
		want        int32
	}{
		{maxAttempts: 1, want: 1},
		{maxAttempts: 0, want: 3},
	} {
		attempts.Store(0)
		_, err := Execute(RelayExecuteRequest{
			Body:        ActionExecuteBody{AccountID: accountID, ActionName: "query_loki"},
			MaxAttempts: tc.maxAttempts,
		})
		if err == nil {
			t.Fatalf("MaxAttempts=%d: Execute succeeded against a relay that drops every connection", tc.maxAttempts)
		}
		if got := attempts.Load(); got != tc.want {
			t.Errorf("MaxAttempts=%d: relay got %d attempts, want %d", tc.maxAttempts, got, tc.want)
		}
	}
}
