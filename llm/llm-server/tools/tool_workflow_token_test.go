package tools

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serveAndCaptureHeaders points the workflow client at a test server and returns
// the headers of the last request it received.
func serveAndCaptureHeaders(t *testing.T, token string) *http.Header {
	t.Helper()
	captured := &http.Header{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*captured = r.Header.Clone()
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	origURL, origToken := config.Config.WorkflowServerEndpoint, config.Config.WorkflowServerToken
	config.Config.WorkflowServerEndpoint, config.Config.WorkflowServerToken = srv.URL, token
	t.Cleanup(func() {
		config.Config.WorkflowServerEndpoint, config.Config.WorkflowServerToken = origURL, origToken
	})
	return captured
}

func TestRunbookRequestSendsTheServiceToken(t *testing.T) {
	// The workflow server is moving to reject callers without its service token;
	// every call from here has to carry it.
	headers := serveAndCaptureHeaders(t, "test-token")
	_, err := DoRunbookRequest(http.MethodGet, "workflows", nil, "acct", "tenant", "user")
	require.NoError(t, err)
	assert.Equal(t, "test-token", headers.Get("X-ACTION-TOKEN"))
}

func TestRunbookRequestOmitsAnUnsetServiceToken(t *testing.T) {
	headers := serveAndCaptureHeaders(t, "")
	_, err := DoRunbookRequest(http.MethodGet, "workflows", nil, "acct", "tenant", "user")
	require.NoError(t, err)
	assert.Empty(t, headers.Values("X-ACTION-TOKEN"))
}
