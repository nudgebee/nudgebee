package workspace

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A reachable pod that is slow to answer (first request of a session, a large
// file) must not be given up on: the old 200 ms header / 500 ms total limit
// sent such calls to the k8s proxy, which fails where pods/proxy is not
// allowed, and the file was lost.
func TestWorkspaceFileAPIClient_WaitsForASlowPod(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(1500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := workspaceFileAPIClient.Post(srv.URL+"/api/v1/files/save", "application/json", nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// A pod that cannot be reached still fails fast, so callers outside the
// cluster fall back to the proxy without waiting out the response timeout.
func TestWorkspaceFileAPIClient_FailsFastWhenUnreachable(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close()) // nothing listens there now

	start := time.Now()
	resp, err := workspaceFileAPIClient.Get("http://" + addr + "/api/v1/files")
	if err == nil {
		_ = resp.Body.Close()
	}
	assert.Error(t, err)
	assert.Less(t, time.Since(start), 2*time.Second)
}
