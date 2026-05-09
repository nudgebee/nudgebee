package network

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"nudgebee/runbook/internal/tasks/testutils"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSslTask_Execute(t *testing.T) {
	task := &SslTask{}
	logger := &TestLogger{}
	taskCtx := testutils.NewTestTaskContext("tenant", "account", "user", logger)

	// Start a test TLS server
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintln(w, "Hello")
	}))
	defer ts.Close()

	// Parse URL to get host/port
	u, _ := url.Parse(ts.URL)
	host := u.Hostname()
	port := u.Port()

	t.Run("Valid Certificate (Self-Signed)", func(t *testing.T) {
		params := map[string]any{
			"host": host,
			"port": port,
		}

		res, err := task.Execute(taskCtx, params)
		assert.NoError(t, err)

		resultMap, ok := res.(map[string]any)
		assert.True(t, ok)
		// httptest certs are usually valid for time
		assert.True(t, resultMap["valid"].(bool))
		assert.NotEmpty(t, resultMap["not_after"])
	})

	t.Run("Missing Host", func(t *testing.T) {
		params := map[string]any{
			"port": 443,
		}
		_, err := task.Execute(taskCtx, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "host parameter is required")
	})
}
